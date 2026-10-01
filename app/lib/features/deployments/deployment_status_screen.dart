import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../models/compose_file.dart';
import '../../models/build.dart';
import '../../models/deployment.dart';
import '../../models/deployment_event.dart';
import '../../models/deployment_request.dart';
import '../../models/deployment_revision.dart';
import '../../models/drift_report.dart';
import '../../models/env_var_group.dart' show kEnvironments;
import '../../models/git_repository.dart';
import '../../models/server.dart';
import '../backups/backups_screen.dart';
import 'deployment_widgets.dart';
import 'build_logs_screen.dart';
import '../../theme/app_theme.dart';

/// One deployment's live status and everything you can do to it: the
/// rollout timeline (streamed), per-service progress, scaling, revisions
/// and rollback (to a revision, Compose file version, or Git commit),
/// promotion to another environment, drift detection, pending approvals,
/// and its governance metadata.
class DeploymentStatusScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String deploymentId;
  // Service names known at launch time (e.g. from the deploy preview) —
  // shown until the deployment's own detail loads.
  final List<String> serviceNames;
  // The Compose file this deployment was launched from, when known —
  // superseded by the loaded detail's composeFileId.
  final String? composeFileId;
  // Admins get approve/reject on pending requests (the control plane
  // enforces it either way).
  final bool isAdmin;

  const DeploymentStatusScreen({
    super.key,
    required this.apiClient,
    required this.deploymentId,
    this.serviceNames = const [],
    this.composeFileId,
    this.isAdmin = false,
  });

  @override
  State<DeploymentStatusScreen> createState() => _DeploymentStatusScreenState();
}

class _DeploymentStatusScreenState extends State<DeploymentStatusScreen> {
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;
  final List<DeploymentEvent> _events = [];
  final Set<int> _seenEventIds = {};
  String? _streamError;
  bool _busy = false;
  DeploymentDetail? _detail;
  Object? _detailError;
  int _revisionsGeneration = 0;
  Timer? _refreshDebounce;

  ApiClient get _api => widget.apiClient;

  @override
  void initState() {
    super.initState();
    _loadDetail();
    _connect();
  }

  @override
  void dispose() {
    _refreshDebounce?.cancel();
    _sub?.cancel();
    _channel?.sink.close();
    super.dispose();
  }

  Future<void> _loadDetail() async {
    try {
      final detail = await _api.getDeploymentDetail(widget.deploymentId);
      if (!mounted) return;
      setState(() {
        _detail = detail;
        _detailError = null;
        _revisionsGeneration++;
      });
    } catch (e) {
      if (mounted) setState(() => _detailError = e);
    }
  }

  /// Coalesces bursts of stream events into one detail refresh.
  void _scheduleRefresh() {
    _refreshDebounce?.cancel();
    _refreshDebounce = Timer(const Duration(milliseconds: 600), _loadDetail);
  }

  void _connect() {
    final channel = WebSocketChannel.connect(
      _api.deploymentStreamUri(widget.deploymentId),
    );
    _channel = channel;
    _sub = channel.stream.listen(
      (data) {
        final event = DeploymentEvent.fromJson(
          jsonDecode(data as String) as Map<String, dynamic>,
        );
        if (!_seenEventIds.add(event.id)) return;
        setState(() => _events.add(event));
        // Whole-stack transitions change phase/health/revision/requests.
        if (!event.isServiceEvent) _scheduleRefresh();
      },
      onError: (Object e) {
        if (mounted) setState(() => _streamError = 'Live updates lost: $e');
      },
    );
  }

  /// Latest whole-stack event — whether a rollout is in flight.
  DeploymentEvent? get _latestStackEvent {
    for (var i = _events.length - 1; i >= 0; i--) {
      if (!_events[i].isServiceEvent) return _events[i];
    }
    return null;
  }

  bool get _rolloutInProgress => _latestStackEvent?.isInProgress ?? false;

  String? get _composeFileId =>
      _detail?.deployment.composeFileId ?? widget.composeFileId;

  List<String> get _serviceNames {
    final names = _detail?.serviceNames ?? const [];
    return names.isNotEmpty ? names : widget.serviceNames;
  }

  Future<void> _gated(
    Future<DeploymentActionOutcome> Function(GateOptions gate) action,
    String failurePrefix,
  ) async {
    setState(() => _busy = true);
    try {
      final outcome = await runGatedAction(
        context,
        action,
        failurePrefix: failurePrefix,
      );
      if (outcome != null) await _loadDetail();
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _redeploy() => _gated(
    (gate) => _api.redeployDeployment(widget.deploymentId, gate: gate),
    'Failed to redeploy',
  );

  Future<void> _redeployService(String service) => _gated(
    (gate) => _api.redeployService(widget.deploymentId, service, gate: gate),
    'Failed to redeploy "$service"',
  );

  Future<void> _scaleService(String service) async {
    final current = _detail?.deployment.scales[service];
    final replicas = await showDialog<int>(
      context: context,
      builder: (_) => _ScaleDialog(service: service, current: current ?? 1),
    );
    if (replicas == null || !mounted) return;
    await _gated(
      (gate) =>
          _api.scaleService(widget.deploymentId, service, replicas, gate: gate),
      'Failed to scale "$service"',
    );
  }

  Future<void> _rollbackToRevision(int revision) async {
    final confirmed = await _confirm(
      'Roll back to revision $revision?',
      'This redeploys the whole stack exactly as revision $revision ran it — '
          'same Compose content, variables, and replica counts.',
      'Roll back',
    );
    if (confirmed != true) return;
    await _gated(
      (gate) => _api.rollbackDeployment(
        widget.deploymentId,
        revision: revision,
        gate: gate,
      ),
      'Failed to roll back',
    );
  }

  Future<void> _rollbackMenu() async {
    final target = _detail?.rollbackTarget;
    final choice = await showDialog<String>(
      context: context,
      builder: (context) => SimpleDialog(
        title: const Text('Roll back to...'),
        children: [
          if (target != null)
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop('target'),
              child: ListTile(
                leading: const Icon(Icons.restore),
                title: Text('Last good revision (${target.revision})'),
                subtitle: Text(
                  [
                    humanizePhase(target.status),
                    if (target.gitCommit.isNotEmpty)
                      'commit ${shortCommit(target.gitCommit)}',
                    formatTimestamp(target.createdAt),
                  ].join(' • '),
                ),
              ),
            ),
          SimpleDialogOption(
            onPressed: () => Navigator.of(context).pop('revision'),
            child: const ListTile(
              leading: Icon(Icons.history),
              title: Text('A specific revision...'),
            ),
          ),
          if (_composeFileId != null) ...[
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop('version'),
              child: const ListTile(
                leading: Icon(Icons.layers_outlined),
                title: Text('A Compose file version...'),
              ),
            ),
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop('commit'),
              child: const ListTile(
                leading: Icon(Icons.commit),
                title: Text('A Git commit...'),
                subtitle: Text('For Compose files imported from Git'),
              ),
            ),
          ],
        ],
      ),
    );
    if (!mounted || choice == null) return;
    switch (choice) {
      case 'target':
        await _rollbackToRevision(target!.revision);
      case 'revision':
        await _pickRevisionAndRollback();
      case 'version':
        await _rollbackToVersion();
      case 'commit':
        await _rollbackToCommit();
    }
  }

  Future<void> _pickRevisionAndRollback() async {
    List<DeploymentRevision> revisions;
    try {
      revisions = await _api.listDeploymentRevisions(widget.deploymentId);
    } catch (e) {
      _snack('Failed to load revisions: $e');
      return;
    }
    final current = _detail?.deployment.currentRevision;
    final candidates = revisions
        .where((r) => r.revision != current && r.isRollbackCandidate)
        .toList();
    if (!mounted) return;
    if (candidates.isEmpty) {
      _snack('No earlier revision that ran successfully.');
      return;
    }
    final revision = await showDialog<int>(
      context: context,
      builder: (context) => SimpleDialog(
        title: const Text('Roll back to which revision?'),
        children: [
          for (final r in candidates)
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop(r.revision),
              child: Text(
                'Revision ${r.revision} — ${humanizePhase(r.status)}'
                '${r.gitCommit.isEmpty ? '' : ' • ${shortCommit(r.gitCommit)}'}'
                ' • ${formatTimestamp(r.createdAt)}',
              ),
            ),
        ],
      ),
    );
    if (revision != null) await _rollbackToRevision(revision);
  }

  Future<void> _rollbackToVersion() async {
    final composeFileId = _composeFileId;
    if (composeFileId == null) return;
    List<ComposeFileVersionSummary> versions;
    try {
      versions = await _api.listComposeFileVersions(composeFileId);
    } catch (e) {
      _snack('Failed to load version history: $e');
      return;
    }
    if (!mounted) return;
    if (versions.isEmpty) {
      _snack('No past versions to roll back to yet.');
      return;
    }
    final version = await showDialog<ComposeFileVersionSummary>(
      context: context,
      builder: (context) => SimpleDialog(
        title: const Text('Roll back to which version?'),
        children: [
          for (final v in versions)
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop(v),
              child: Text(
                'v${v.versionNumber} — ${formatTimestamp(v.createdAt)}'
                '${v.gitCommit.isEmpty ? '' : ' • ${shortCommit(v.gitCommit)}'}',
              ),
            ),
        ],
      ),
    );
    if (version == null || !mounted) return;
    final confirmed = await _confirm(
      'Roll back to v${version.versionNumber}?',
      'This redeploys the whole stack using that version\'s Compose content. '
          'The Compose file itself is unchanged.',
      'Roll back',
    );
    if (confirmed != true) return;
    await _gated(
      (gate) => _api.rollbackDeployment(
        widget.deploymentId,
        versionId: version.id,
        gate: gate,
      ),
      'Failed to roll back',
    );
  }

  Future<void> _rollbackToCommit() async {
    final composeFileId = _composeFileId;
    if (composeFileId == null) return;
    setState(() => _busy = true);
    List<GitCommit> commits;
    try {
      final gitRef = _detail?.deployment.gitRef ?? '';
      commits = await _api.listComposeFileCommits(composeFileId, ref: gitRef);
    } catch (e) {
      _snack('Couldn\'t load commits: ${e is ApiException ? e.message : e}');
      return;
    } finally {
      if (mounted) setState(() => _busy = false);
    }
    if (!mounted) return;
    if (commits.isEmpty) {
      _snack('No commits found for this file.');
      return;
    }
    final commit = await showDialog<GitCommit>(
      context: context,
      builder: (context) => SimpleDialog(
        title: const Text('Roll back to which commit?'),
        children: [
          for (final c in commits)
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop(c),
              child: ListTile(
                dense: true,
                leading: Text(
                  shortCommit(c.hash),
                  style: const TextStyle(fontFamily: 'monospace'),
                ),
                title: Text(
                  c.message,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                subtitle: Text('${c.author} • ${formatTimestamp(c.date)}'),
              ),
            ),
        ],
      ),
    );
    if (commit == null || !mounted) return;
    final confirmed = await _confirm(
      'Roll back to commit ${shortCommit(commit.hash)}?',
      'This redeploys the whole stack using the Compose file as it was at '
          'that commit. The linked Compose file itself is unchanged.',
      'Roll back',
    );
    if (confirmed != true) return;
    await _gated(
      (gate) => _api.rollbackDeployment(
        widget.deploymentId,
        gitCommit: commit.hash,
        gate: gate,
      ),
      'Failed to roll back',
    );
  }

  Future<void> _promote() async {
    final detail = _detail;
    if (detail == null) return;
    if (detail.deployment.currentRevision == 0) {
      _snack('Deploy this first — there is no revision to promote yet.');
      return;
    }
    List<Server> servers;
    try {
      servers = await _api.listServers();
    } catch (e) {
      _snack('Failed to load servers: $e');
      return;
    }
    if (!mounted) return;
    final request = await showDialog<_PromoteRequest>(
      context: context,
      builder: (_) => _PromoteDialog(
        servers: servers,
        currentServerId: detail.deployment.serverId,
        currentEnvironment: detail.deployment.deployEnvironment,
        revision: detail.deployment.currentRevision,
      ),
    );
    if (request == null || !mounted) return;
    setState(() => _busy = true);
    try {
      final outcome = await runGatedAction(
        context,
        (gate) => _api.promoteDeployment(
          widget.deploymentId,
          serverId: request.serverId,
          environment: request.environment,
          metadata: request.metadata,
          gate: gate,
        ),
        failurePrefix: 'Failed to promote',
      );
      if (outcome != null && mounted) {
        await Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => DeploymentStatusScreen(
              apiClient: _api,
              deploymentId: outcome.deploymentId,
              isAdmin: widget.isAdmin,
            ),
          ),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _editMetadata() async {
    final detail = _detail;
    if (detail == null) return;
    final result = await showDialog<Deployment>(
      context: context,
      builder: (_) => _MetadataDialog(
        apiClient: _api,
        existing: detail.deployment,
        isGitSource: _composeFileId != null,
      ),
    );
    if (result != null) await _loadDetail();
  }

  Future<void> _openBackups() async {
    final restoreTriggered = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) =>
            BackupsScreen(apiClient: _api, deploymentId: widget.deploymentId),
      ),
    );
    if (restoreTriggered == true) await _loadDetail();
  }

  Future<void> _runStackAction(String action) async {
    if (action == 'remove') {
      final confirmed = await _confirm(
        'Remove this stack?',
        'This stops and removes every container in this deployment and its '
            'networks. Named volumes are kept.',
        'Remove',
        destructive: true,
      );
      if (confirmed != true) return;
    }
    setState(() => _busy = true);
    try {
      final result = await _api.deploymentAction(widget.deploymentId, action);
      final ok = action == 'remove'
          ? (result['success'] as bool? ?? false)
          : (result['results'] as List<dynamic>? ?? []).every(
              (r) => (r as Map<String, dynamic>)['success'] == true,
            );
      _snack(
        ok
            ? 'Stack $action succeeded.'
            : 'Stack $action failed for one or more containers.',
      );
      await _loadDetail();
    } catch (e) {
      _snack('Failed to $action stack: ${e is ApiException ? e.message : e}');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _approve(DeploymentRequest r) async {
    final comment = await promptComment(
      context,
      title: 'Approve: ${r.summary}',
      action: 'Approve',
    );
    if (comment == null || !mounted) return;
    setState(() => _busy = true);
    try {
      final outcome = await _api.approveDeploymentRequest(
        r.id,
        comment: comment,
      );
      _snack(outcome.describe());
      await _loadDetail();
    } catch (e) {
      _snack('Failed to approve: ${e is ApiException ? e.message : e}');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _reject(DeploymentRequest r) async {
    final comment = await promptComment(
      context,
      title: 'Reject: ${r.summary}',
      action: 'Reject',
      hint: 'Reason',
      destructive: true,
    );
    if (comment == null || !mounted) return;
    await _decide(
      () => _api.rejectDeploymentRequest(r.id, comment: comment),
      'Request rejected.',
    );
  }

  Future<void> _cancelRequest(DeploymentRequest r) async {
    final comment = await promptComment(
      context,
      title: 'Cancel: ${r.summary}',
      action: 'Cancel request',
      destructive: true,
    );
    if (comment == null || !mounted) return;
    await _decide(
      () => _api.cancelDeploymentRequest(r.id, comment: comment),
      'Request cancelled.',
    );
  }

  Future<void> _decide(Future<void> Function() call, String done) async {
    setState(() => _busy = true);
    try {
      await call();
      _snack(done);
      await _loadDetail();
    } catch (e) {
      _snack('Failed: ${e is ApiException ? e.message : e}');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<bool?> _confirm(
    String title,
    String body,
    String action, {
    bool destructive = false,
  }) {
    return showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(title),
        content: Text(body),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: destructive
                ? FilledButton.styleFrom(
                    backgroundColor: Theme.of(context).colorScheme.error,
                  )
                : null,
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(action),
          ),
        ],
      ),
    );
  }

  void _snack(String text) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
  }

  @override
  Widget build(BuildContext context) {
    final detail = _detail;
    final inProgress = _rolloutInProgress;
    final actionsEnabled = !_busy && !inProgress && detail != null;

    return DefaultTabController(
      length: 5,
      child: Scaffold(
        appBar: AppBar(
          title: Text(
            detail == null || detail.sourceName.isEmpty
                ? 'Deployment status'
                : detail.sourceName,
          ),
          actions: [
            if (_busy)
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 12),
                child: Center(
                  child: SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                ),
              ),
            IconButton(
              icon: const Icon(Icons.refresh),
              tooltip: 'Refresh',
              onPressed: _loadDetail,
            ),
            IconButton(
              icon: const Icon(Icons.edit_note_outlined),
              tooltip: 'Environment, change request, rollout settings',
              onPressed: detail == null ? null : _editMetadata,
            ),
            IconButton(
              icon: const Icon(Icons.backup),
              tooltip: 'Backups',
              onPressed: _openBackups,
            ),
            const SizedBox(width: 12),
          ],
          bottom: const TabBar(
            isScrollable: true,
            tabAlignment: TabAlignment.start,
            tabs: [
              Tab(text: 'Timeline'),
              Tab(text: 'Services'),
              Tab(text: 'Builds'),
              Tab(text: 'Revisions'),
              Tab(text: 'Drift'),
            ],
          ),
        ),
        body: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (detail != null) ...[
              _Header(detail: detail, inProgress: inProgress),
              _ActionRow(
                // Nothing to redeploy, roll back, or promote until the
                // first rollout has run (e.g. while awaiting approval).
                enabled:
                    actionsEnabled && detail.deployment.currentRevision > 0,
                rolling: detail.deployment.updateStrategy == 'rolling',
                onRedeploy: _redeploy,
                onRollback: _rollbackMenu,
                onPromote: _promote,
                onStackAction: _runStackAction,
              ),
            ] else if (_detailError != null)
              Padding(
                padding: const EdgeInsets.all(16),
                child: Text(
                  'Failed to load deployment: $_detailError',
                  style: const TextStyle(color: AppColors.failed),
                ),
              )
            else
              const LinearProgressIndicator(),
            if (detail != null)
              for (final r in detail.openRequests)
                _RequestBanner(
                  request: r,
                  isAdmin: widget.isAdmin,
                  onApprove: _busy ? null : () => _approve(r),
                  onReject: _busy ? null : () => _reject(r),
                  onCancel: _busy ? null : () => _cancelRequest(r),
                ),
            if (_streamError != null)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Text(
                  _streamError!,
                  style: const TextStyle(color: AppColors.failed),
                ),
              ),
            const Divider(height: 1),
            Expanded(
              child: TabBarView(
                children: [
                  _TimelineTab(events: _events),
                  _ServicesTab(
                    serviceNames: _serviceNames,
                    progress: latestServiceProgress(_events),
                    scales: detail?.deployment.scales ?? const {},
                    enabled: actionsEnabled,
                    onScale: _scaleService,
                    onRedeploy: _redeployService,
                  ),
                  _BuildsTab(
                    key: ValueKey('builds-$_revisionsGeneration'),
                    apiClient: _api,
                    deploymentId: widget.deploymentId,
                    isAdmin: widget.isAdmin,
                  ),
                  _RevisionsTab(
                    key: ValueKey(_revisionsGeneration),
                    apiClient: _api,
                    deploymentId: widget.deploymentId,
                    currentRevision: detail?.deployment.currentRevision ?? 0,
                    enabled: actionsEnabled,
                    onRollback: _rollbackToRevision,
                  ),
                  _DriftTab(apiClient: _api, deploymentId: widget.deploymentId),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _BuildsTab extends StatefulWidget {
  final ApiClient apiClient;
  final String deploymentId;
  final bool isAdmin;

  const _BuildsTab({
    super.key,
    required this.apiClient,
    required this.deploymentId,
    required this.isAdmin,
  });

  @override
  State<_BuildsTab> createState() => _BuildsTabState();
}

class _BuildsTabState extends State<_BuildsTab> {
  late Future<List<ImageBuild>> _future = _load();

  Future<List<ImageBuild>> _load() =>
      widget.apiClient.listDeploymentBuilds(widget.deploymentId);

  void _refresh() => setState(() => _future = _load());

  @override
  Widget build(BuildContext context) => FutureBuilder<List<ImageBuild>>(
    future: _future,
    builder: (context, snapshot) {
      if (snapshot.hasError) {
        return Center(child: Text('Could not load builds: ${snapshot.error}'));
      }
      if (!snapshot.hasData) {
        return const Center(child: CircularProgressIndicator());
      }
      final builds = snapshot.data!;
      if (builds.isEmpty) {
        return const Center(
          child: Text('No image builds for this deployment.'),
        );
      }
      return RefreshIndicator(
        onRefresh: () async => _refresh(),
        child: ListView.builder(
          itemCount: builds.length,
          itemBuilder: (context, index) {
            final build = builds[index];
            return ListTile(
              leading: Icon(
                Icons.build_outlined,
                color: phaseColor(build.status),
              ),
              title: Text('${build.service} · ${humanizePhase(build.status)}'),
              subtitle: Text(
                'Revision ${build.revision} · ${build.imageTag}'
                '${build.reused ? ' · reused' : ''}',
              ),
              trailing: const Icon(Icons.chevron_right),
              onTap: () async {
                await Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => BuildLogsScreen(
                      apiClient: widget.apiClient,
                      initialBuild: build,
                      isAdmin: widget.isAdmin,
                    ),
                  ),
                );
                if (mounted) _refresh();
              },
            );
          },
        ),
      );
    },
  );
}

class _Header extends StatelessWidget {
  final DeploymentDetail detail;
  final bool inProgress;

  const _Header({required this.detail, required this.inProgress});

  @override
  Widget build(BuildContext context) {
    final d = detail.deployment;
    final rev = detail.currentRevision;
    final small = Theme.of(context).textTheme.bodySmall;
    final chips = <Widget>[
      HealthBadge(status: d.healthStatus, message: d.healthMessage),
      if (d.currentRevision > 0)
        _chip(Icons.tag, 'Revision ${d.currentRevision}'),
      if (rev != null && rev.gitCommit.isNotEmpty)
        _chip(
          Icons.commit,
          '${rev.gitRef.isEmpty ? '' : '${rev.gitRef} @ '}${shortCommit(rev.gitCommit)}',
        ),
      if (d.deployEnvironment != null && d.deployEnvironment!.isNotEmpty)
        _chip(Icons.public, d.deployEnvironment!),
      _chip(
        d.updateStrategy == 'rolling' ? Icons.swap_horiz : Icons.autorenew,
        d.updateStrategy == 'rolling' ? 'Rolling updates' : 'Recreate',
      ),
      if (d.autoRollback) _chip(Icons.restore, 'Auto-rollback'),
      if (d.autoDeploy) _chip(Icons.webhook, 'Auto-deploy on push'),
      if (d.changeRequest.isNotEmpty) _chip(Icons.link, d.changeRequest),
      for (final tag in d.tags) Chip(label: Text(tag)),
      if (detail.policy != null)
        for (final rule in detail.policy!.ruleLabels)
          _chip(Icons.policy_outlined, rule),
    ];
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.circle, size: 12, color: phaseColor(d.phase)),
              const SizedBox(width: 8),
              Text(
                humanizePhase(d.phase).toUpperCase(),
                style: Theme.of(context).textTheme.titleMedium,
              ),
              if (inProgress) ...[
                const SizedBox(width: 12),
                const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              ],
            ],
          ),
          const SizedBox(height: 8),
          Wrap(spacing: 8, runSpacing: 4, children: chips),
          if (d.healthStatus == 'unhealthy' && d.healthMessage.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              d.healthMessage,
              style: small?.copyWith(
                color: Theme.of(context).colorScheme.error,
              ),
            ),
          ],
          if (d.rollbackPlan.isNotEmpty || detail.rollbackTarget != null)
            const SizedBox(height: 4),
          if (d.rollbackPlan.isNotEmpty)
            InfoLine(
              icon: Icons.assignment_return_outlined,
              label: 'Rollback plan',
              value: d.rollbackPlan,
            ),
          if (detail.rollbackTarget != null)
            InfoLine(
              icon: Icons.restore,
              label: 'Rollback target',
              value:
                  'revision ${detail.rollbackTarget!.revision} '
                  '(${humanizePhase(detail.rollbackTarget!.status)}, '
                  '${formatTimestamp(detail.rollbackTarget!.createdAt)})',
            ),
          if (d.notes.isNotEmpty)
            InfoLine(icon: Icons.notes, label: 'Notes', value: d.notes),
        ],
      ),
    );
  }

  Widget _chip(IconData icon, String label) => Chip(
    visualDensity: VisualDensity.compact,
    avatar: Icon(icon, size: 14),
    label: Text(label),
  );
}

/// The deployment's primary actions as labelled buttons — easier to find
/// than icon-only app bar actions, and clear of the window's top corner.
class _ActionRow extends StatelessWidget {
  final bool enabled;
  final bool rolling;
  final VoidCallback onRedeploy;
  final VoidCallback onRollback;
  final VoidCallback onPromote;
  final ValueChanged<String> onStackAction;

  const _ActionRow({
    required this.enabled,
    required this.rolling,
    required this.onRedeploy,
    required this.onRollback,
    required this.onPromote,
    required this.onStackAction,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
      child: Wrap(
        spacing: 8,
        runSpacing: 8,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          FilledButton.tonalIcon(
            onPressed: enabled ? onRedeploy : null,
            icon: const Icon(Icons.replay, size: 18),
            label: Text(rolling ? 'Redeploy (rolling)' : 'Redeploy'),
          ),
          OutlinedButton.icon(
            onPressed: enabled ? onRollback : null,
            icon: const Icon(Icons.restore, size: 18),
            label: const Text('Roll back'),
          ),
          OutlinedButton.icon(
            onPressed: enabled ? onPromote : null,
            icon: const Icon(Icons.publish_outlined, size: 18),
            label: const Text('Promote'),
          ),
          MenuAnchor(
            menuChildren: [
              for (final (value, label, icon) in const [
                ('start', 'Start stack', Icons.play_arrow_outlined),
                ('stop', 'Stop stack', Icons.stop_outlined),
                ('restart', 'Restart stack', Icons.restart_alt),
                ('remove', 'Remove stack', Icons.delete_outline),
              ])
                MenuItemButton(
                  leadingIcon: Icon(icon, size: 18),
                  onPressed: () => onStackAction(value),
                  child: Text(label),
                ),
            ],
            builder: (context, controller, _) => OutlinedButton.icon(
              onPressed: enabled
                  ? () => controller.isOpen
                        ? controller.close()
                        : controller.open()
                  : null,
              icon: const Icon(Icons.layers_outlined, size: 18),
              label: const Text('Stack'),
            ),
          ),
        ],
      ),
    );
  }
}

/// An open approval/scheduled request on this deployment.
class _RequestBanner extends StatelessWidget {
  final DeploymentRequest request;
  final bool isAdmin;
  final VoidCallback? onApprove;
  final VoidCallback? onReject;
  final VoidCallback? onCancel;

  const _RequestBanner({
    required this.request,
    required this.isAdmin,
    this.onApprove,
    this.onReject,
    this.onCancel,
  });

  @override
  Widget build(BuildContext context) {
    final pending = request.status == 'pending_approval';
    final title = pending
        ? 'Waiting for approval: ${request.summary}'
        : 'Scheduled: ${request.summary}'
              '${request.scheduledFor == null ? '' : ' at ${formatTimestamp(request.scheduledFor!)}'}';
    return MaterialBanner(
      leading: Icon(
        pending ? Icons.how_to_reg_outlined : Icons.schedule,
        color: phaseColor(request.status),
      ),
      content: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title),
          Text(
            [
              if (request.requestedByEmail != null)
                'requested by ${request.requestedByEmail}',
              formatTimestamp(request.requestedAt),
              if (request.reason.isNotEmpty) request.reason,
            ].join(' • '),
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
      ),
      actions: [
        TextButton(onPressed: onCancel, child: const Text('Cancel request')),
        if (pending && isAdmin) ...[
          TextButton(onPressed: onReject, child: const Text('Reject')),
          FilledButton(onPressed: onApprove, child: const Text('Approve')),
        ],
      ],
    );
  }
}

class _TimelineTab extends StatelessWidget {
  final List<DeploymentEvent> events;

  const _TimelineTab({required this.events});

  String _time(DateTime t) {
    final l = t.toLocal();
    String two(int n) => n.toString().padLeft(2, '0');
    return '${two(l.hour)}:${two(l.minute)}:${two(l.second)}';
  }

  @override
  Widget build(BuildContext context) {
    if (events.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    final reversed = events.reversed.toList();
    return ListView.builder(
      itemCount: reversed.length,
      itemBuilder: (context, index) {
        final e = reversed[index];
        final subtitle = [
          if (e.message.isNotEmpty) e.message,
          if (e.triggeredByEmail != null) 'by ${e.triggeredByEmail}',
        ].join(' — ');
        return ListTile(
          dense: true,
          contentPadding: EdgeInsets.only(
            left: e.isServiceEvent ? 40 : 16,
            right: 16,
          ),
          leading: Icon(Icons.circle, size: 10, color: phaseColor(e.phase)),
          title: Text(
            e.isServiceEvent
                ? '${e.service}: ${humanizePhase(e.phase)}'
                : humanizePhase(e.phase),
          ),
          subtitle: subtitle.isEmpty ? null : Text(subtitle),
          trailing: Text(
            _time(e.createdAt),
            style: Theme.of(context).textTheme.bodySmall,
          ),
        );
      },
    );
  }
}

class _ServicesTab extends StatelessWidget {
  final List<String> serviceNames;
  final List<ServiceProgress> progress;
  final Map<String, int> scales;
  final bool enabled;
  final ValueChanged<String> onScale;
  final ValueChanged<String> onRedeploy;

  const _ServicesTab({
    required this.serviceNames,
    required this.progress,
    required this.scales,
    required this.enabled,
    required this.onScale,
    required this.onRedeploy,
  });

  @override
  Widget build(BuildContext context) {
    final byService = {for (final p in progress) p.service: p};
    // Services in rollout order first (dependency order), then any others.
    final names = [
      for (final p in progress) p.service,
      for (final n in serviceNames)
        if (!byService.containsKey(n)) n,
    ];
    if (names.isEmpty) {
      return const Center(child: Text('No services known yet.'));
    }
    return ListView(
      children: [
        for (final name in names)
          ListTile(
            leading: Icon(
              Icons.circle,
              size: 12,
              color: phaseColor(byService[name]?.phase ?? 'unknown'),
            ),
            title: Text(name),
            subtitle: Text(
              [
                if (byService[name] != null)
                  '${humanizePhase(byService[name]!.phase)}'
                      '${byService[name]!.message.isEmpty ? '' : ' — ${byService[name]!.message}'}',
                scales[name] == null
                    ? 'Replicas: as declared'
                    : 'Replicas: ${scales[name]}',
              ].join('\n'),
            ),
            isThreeLine: byService[name] != null,
            trailing: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextButton.icon(
                  icon: const Icon(Icons.stacked_bar_chart, size: 18),
                  label: const Text('Scale'),
                  onPressed: enabled ? () => onScale(name) : null,
                ),
                const SizedBox(width: 4),
                TextButton.icon(
                  icon: const Icon(Icons.replay, size: 18),
                  label: const Text('Redeploy'),
                  onPressed: enabled ? () => onRedeploy(name) : null,
                ),
              ],
            ),
          ),
      ],
    );
  }
}

class _RevisionsTab extends StatefulWidget {
  final ApiClient apiClient;
  final String deploymentId;
  final int currentRevision;
  final bool enabled;
  final ValueChanged<int> onRollback;

  const _RevisionsTab({
    super.key,
    required this.apiClient,
    required this.deploymentId,
    required this.currentRevision,
    required this.enabled,
    required this.onRollback,
  });

  @override
  State<_RevisionsTab> createState() => _RevisionsTabState();
}

class _RevisionsTabState extends State<_RevisionsTab> {
  late final Future<List<DeploymentRevision>> _future = widget.apiClient
      .listDeploymentRevisions(widget.deploymentId);

  Future<void> _view(DeploymentRevision r) async {
    try {
      final full = await widget.apiClient.getDeploymentRevision(
        widget.deploymentId,
        r.revision,
      );
      if (mounted) {
        await showYamlDialog(
          context,
          'Revision ${r.revision}',
          full.composeContent,
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to load revision: $e')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<List<DeploymentRevision>>(
      future: _future,
      builder: (context, snapshot) {
        if (snapshot.connectionState != ConnectionState.done) {
          return const Center(child: CircularProgressIndicator());
        }
        if (snapshot.hasError) {
          return Center(
            child: Text('Failed to load revisions: ${snapshot.error}'),
          );
        }
        final revisions = snapshot.data ?? const [];
        if (revisions.isEmpty) {
          return const Center(child: Text('Nothing has been deployed yet.'));
        }
        return ListView(
          children: [
            for (final r in revisions)
              ListTile(
                leading: CircleAvatar(
                  radius: 16,
                  backgroundColor: phaseColor(r.status).withValues(alpha: 0.15),
                  child: Text(
                    '${r.revision}',
                    style: TextStyle(color: phaseColor(r.status)),
                  ),
                ),
                title: Text(
                  '${humanizePhase(r.action)} — ${humanizePhase(r.status)}'
                  '${r.revision == widget.currentRevision ? ' (current)' : ''}',
                ),
                subtitle: Text(
                  [
                    [
                      formatTimestamp(r.createdAt),
                      r.createdByEmail ?? 'system',
                      if (r.composeVersion != null)
                        'Compose v${r.composeVersion}',
                      if (r.gitCommit.isNotEmpty)
                        '${r.gitRef.isEmpty ? '' : '${r.gitRef} @ '}${shortCommit(r.gitCommit)}',
                      if (r.strategy == 'rolling') 'rolling',
                    ].join(' • '),
                    if (r.statusMessage.isNotEmpty) r.statusMessage,
                  ].join('\n'),
                ),
                isThreeLine: r.statusMessage.isNotEmpty,
                trailing: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    TextButton.icon(
                      icon: const Icon(Icons.description_outlined, size: 18),
                      label: const Text('View'),
                      onPressed: () => _view(r),
                    ),
                    const SizedBox(width: 4),
                    TextButton.icon(
                      icon: const Icon(Icons.restore, size: 18),
                      label: const Text('Roll back'),
                      onPressed:
                          widget.enabled &&
                              r.revision != widget.currentRevision &&
                              r.isRollbackCandidate
                          ? () => widget.onRollback(r.revision)
                          : null,
                    ),
                  ],
                ),
              ),
          ],
        );
      },
    );
  }
}

class _DriftTab extends StatefulWidget {
  final ApiClient apiClient;
  final String deploymentId;

  const _DriftTab({required this.apiClient, required this.deploymentId});

  @override
  State<_DriftTab> createState() => _DriftTabState();
}

class _DriftTabState extends State<_DriftTab> {
  Future<DriftReport>? _future;

  void _check() {
    setState(() {
      _future = widget.apiClient.getDeploymentDrift(widget.deploymentId);
    });
  }

  @override
  Widget build(BuildContext context) {
    final future = _future;
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                'Compares what\'s running against the current revision, the '
                'Compose file, and (for Git-linked files) the tracked branch.',
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
            const SizedBox(width: 12),
            FilledButton.icon(
              onPressed: _check,
              icon: const Icon(Icons.compare_arrows),
              label: Text(future == null ? 'Check for drift' : 'Check again'),
            ),
          ],
        ),
        const SizedBox(height: 16),
        if (future != null)
          FutureBuilder<DriftReport>(
            future: future,
            builder: (context, snapshot) {
              if (snapshot.connectionState != ConnectionState.done) {
                return const Center(child: CircularProgressIndicator());
              }
              if (snapshot.hasError) {
                return Text('Drift check failed: ${snapshot.error}');
              }
              return _DriftReportView(report: snapshot.data!);
            },
          ),
      ],
    );
  }
}

class _DriftReportView extends StatelessWidget {
  final DriftReport report;

  const _DriftReportView({required this.report});

  Widget _section(
    BuildContext context,
    String title,
    bool drifted,
    List<Widget> body,
  ) {
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  drifted ? Icons.warning_amber : Icons.check_circle_outline,
                  size: 18,
                  color: drifted ? AppColors.warning : AppColors.healthy,
                ),
                const SizedBox(width: 8),
                Text(title, style: Theme.of(context).textTheme.titleSmall),
              ],
            ),
            const SizedBox(height: 6),
            ...body,
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final config = report.config;
    final git = report.git;
    final runtime = report.runtime;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          report.drifted ? 'Drift detected' : 'No drift — everything matches',
          style: Theme.of(context).textTheme.titleMedium?.copyWith(
            color: report.drifted ? AppColors.warning : AppColors.healthy,
          ),
        ),
        Text(
          'Checked ${formatTimestamp(report.checkedAt)}',
          style: Theme.of(context).textTheme.bodySmall,
        ),
        const SizedBox(height: 8),
        _section(context, 'Configuration', config.drifted, [
          Text(
            config.drifted
                ? config.reason
                : 'Revision ${config.deployedRevision} matches the current definition.',
          ),
          if (config.deployedContent.isNotEmpty)
            Align(
              alignment: Alignment.centerLeft,
              child: TextButton(
                onPressed: () => showDialog<void>(
                  context: context,
                  builder: (_) => _SideBySideDialog(
                    left: config.deployedContent,
                    right: config.currentContent,
                  ),
                ),
                child: const Text('Compare deployed vs current'),
              ),
            ),
        ]),
        if (git != null)
          _section(context, 'Git (${git.ref})', git.drifted, [
            if (git.error.isNotEmpty)
              Text('Couldn\'t check: ${git.error}')
            else if (git.drifted)
              Text(
                'Deployed ${shortCommit(git.deployedCommit)}, but ${git.ref} is '
                'now at ${shortCommit(git.latestCommit)}.',
              )
            else
              Text(
                'Deployed commit ${shortCommit(git.deployedCommit)} is the latest on ${git.ref}.',
              ),
          ]),
        _section(context, 'Running containers', runtime.drifted, [
          if (!runtime.drifted)
            const Text(
              'Every expected container is running with the expected image.',
            ),
          for (final m in runtime.missing) Text('• Missing: $m'),
          for (final m in runtime.notRunning) Text('• Not running: $m'),
          for (final m in runtime.imageMismatch) Text('• Image drift: $m'),
          for (final m in runtime.unexpected)
            Text('• Unexpected container: $m'),
          if (runtime.inventoryAt != null)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(
                'Container inventory as of ${formatTimestamp(runtime.inventoryAt!)} '
                '(reported by the server\'s agent).',
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
        ]),
      ],
    );
  }
}

class _SideBySideDialog extends StatelessWidget {
  final String left;
  final String right;

  const _SideBySideDialog({required this.left, required this.right});

  @override
  Widget build(BuildContext context) {
    Widget pane(String title, String text) => Expanded(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(title, style: Theme.of(context).textTheme.titleSmall),
          const SizedBox(height: 4),
          Expanded(
            child: Container(
              padding: const EdgeInsets.all(8),
              decoration: BoxDecoration(
                border: Border.all(color: Theme.of(context).dividerColor),
              ),
              child: SingleChildScrollView(
                child: SelectableText(
                  text,
                  style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
                ),
              ),
            ),
          ),
        ],
      ),
    );
    return AlertDialog(
      title: const Text('Deployed vs current'),
      content: SizedBox(
        width: 900,
        height: 520,
        child: Row(
          children: [
            pane('Deployed (current revision)', left),
            const SizedBox(width: 12),
            pane('Current definition', right),
          ],
        ),
      ),
      actions: [
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
      ],
    );
  }
}

class _ScaleDialog extends StatefulWidget {
  final String service;
  final int current;

  const _ScaleDialog({required this.service, required this.current});

  @override
  State<_ScaleDialog> createState() => _ScaleDialogState();
}

class _ScaleDialogState extends State<_ScaleDialog> {
  late int _replicas = widget.current;

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('Scale ${widget.service}'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              IconButton(
                icon: const Icon(Icons.remove),
                onPressed: _replicas > 1
                    ? () => setState(() => _replicas--)
                    : null,
              ),
              Text(
                '$_replicas',
                style: Theme.of(context).textTheme.headlineMedium,
              ),
              IconButton(
                icon: const Icon(Icons.add),
                onPressed: _replicas < 50
                    ? () => setState(() => _replicas++)
                    : null,
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            'Existing containers keep running; only the difference is added '
            'or removed. A service that publishes a fixed host port can only '
            'run one replica.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(_replicas),
          child: const Text('Scale'),
        ),
      ],
    );
  }
}

class _PromoteRequest {
  final String serverId;
  final String environment;
  final DeploymentMetadata metadata;

  const _PromoteRequest({
    required this.serverId,
    required this.environment,
    required this.metadata,
  });
}

/// "Promote" a deployment's current revision to another environment.
class _PromoteDialog extends StatefulWidget {
  final List<Server> servers;
  final String currentServerId;
  final String? currentEnvironment;
  final int revision;

  const _PromoteDialog({
    required this.servers,
    required this.currentServerId,
    required this.currentEnvironment,
    required this.revision,
  });

  @override
  State<_PromoteDialog> createState() => _PromoteDialogState();
}

class _PromoteDialogState extends State<_PromoteDialog> {
  late String? _serverId =
      widget.servers.any((s) => s.id == widget.currentServerId)
      ? widget.currentServerId
      : null;
  late String? _environment = _nextEnvironment();
  final _changeRequest = TextEditingController();
  final _rollbackPlan = TextEditingController();
  final _notes = TextEditingController();
  bool _autoRollback = true;

  String? _nextEnvironment() {
    final i = kEnvironments.indexOf(widget.currentEnvironment ?? '');
    if (i >= 0 && i + 1 < kEnvironments.length) return kEnvironments[i + 1];
    return null;
  }

  @override
  void dispose() {
    _changeRequest.dispose();
    _rollbackPlan.dispose();
    _notes.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('Promote revision ${widget.revision}'),
      content: SizedBox(
        width: 460,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(
                'Creates a new deployment that runs exactly what revision '
                '${widget.revision} runs (same Compose content, commit, and '
                'replica counts), through the target environment\'s policy.',
                style: Theme.of(context).textTheme.bodySmall,
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: _environment,
                decoration: const InputDecoration(
                  labelText: 'Target environment',
                  isDense: true,
                ),
                items: [
                  for (final env in kEnvironments)
                    DropdownMenuItem(value: env, child: Text(env)),
                ],
                onChanged: (v) => setState(() => _environment = v),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: _serverId,
                decoration: const InputDecoration(
                  labelText: 'Target server',
                  isDense: true,
                ),
                items: [
                  for (final s in widget.servers)
                    DropdownMenuItem(
                      value: s.id,
                      child: Text('${s.name} (${s.status})'),
                    ),
                ],
                onChanged: (v) => setState(() => _serverId = v),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _changeRequest,
                decoration: const InputDecoration(
                  labelText: 'Change request reference',
                  hintText: 'e.g. CHG-2001',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _rollbackPlan,
                maxLines: 2,
                decoration: const InputDecoration(
                  labelText: 'Rollback plan',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _notes,
                maxLines: 2,
                decoration: const InputDecoration(
                  labelText: 'Notes',
                  isDense: true,
                ),
              ),
              CheckboxListTile(
                contentPadding: EdgeInsets.zero,
                value: _autoRollback,
                onChanged: (v) => setState(() => _autoRollback = v ?? false),
                title: const Text('Roll back automatically if unhealthy'),
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _serverId == null || _environment == null
              ? null
              : () => Navigator.of(context).pop(
                  _PromoteRequest(
                    serverId: _serverId!,
                    environment: _environment!,
                    metadata: DeploymentMetadata(
                      environment: _environment,
                      changeRequest: _changeRequest.text.trim(),
                      rollbackPlan: _rollbackPlan.text.trim(),
                      notes: _notes.text.trim(),
                      autoRollback: _autoRollback,
                    ),
                  ),
                ),
          child: const Text('Promote'),
        ),
      ],
    );
  }
}

/// Edits a deployment's governance and rollout metadata: environment,
/// change request, notes, rollback plan, auto-rollback, update strategy,
/// and (for Git-linked Compose files) the tracked branch/tag and
/// auto-deploy.
class _MetadataDialog extends StatefulWidget {
  final ApiClient apiClient;
  final Deployment existing;
  final bool isGitSource;

  const _MetadataDialog({
    required this.apiClient,
    required this.existing,
    required this.isGitSource,
  });

  @override
  State<_MetadataDialog> createState() => _MetadataDialogState();
}

class _MetadataDialogState extends State<_MetadataDialog> {
  late String? _environment = widget.existing.deployEnvironment;
  late final _changeRequest = TextEditingController(
    text: widget.existing.changeRequest,
  );
  late final _notes = TextEditingController(text: widget.existing.notes);
  late final _rollbackPlan = TextEditingController(
    text: widget.existing.rollbackPlan,
  );
  late final _gitRef = TextEditingController(text: widget.existing.gitRef);
  late bool _autoRollback = widget.existing.autoRollback;
  late bool _autoDeploy = widget.existing.autoDeploy;
  late String _strategy = widget.existing.updateStrategy;
  bool _saving = false;
  String? _error;

  @override
  void dispose() {
    _changeRequest.dispose();
    _notes.dispose();
    _rollbackPlan.dispose();
    _gitRef.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    setState(() {
      _saving = true;
      _error = null;
    });
    final metadata = DeploymentMetadata(
      environment: _environment,
      tags: widget.existing.tags,
      changeRequest: _changeRequest.text.trim(),
      notes: _notes.text.trim(),
      rollbackPlan: _rollbackPlan.text.trim(),
      autoRollback: _autoRollback,
      updateStrategy: _strategy,
      gitRef: _gitRef.text.trim(),
      autoDeploy: _autoDeploy,
    );
    try {
      await widget.apiClient.updateDeploymentMetadata(
        widget.existing.id,
        metadata,
      );
      if (mounted) {
        Navigator.of(context).pop(widget.existing.copyWithMetadata(metadata));
      }
    } catch (e) {
      setState(
        () => _error = 'Failed to save: ${e is ApiException ? e.message : e}',
      );
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Deployment settings'),
      content: SizedBox(
        width: 460,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              DropdownButtonFormField<String?>(
                initialValue: _environment,
                decoration: const InputDecoration(
                  labelText: 'Environment',
                  isDense: true,
                ),
                items: [
                  const DropdownMenuItem(value: null, child: Text('None')),
                  for (final env in kEnvironments)
                    DropdownMenuItem(value: env, child: Text(env)),
                ],
                onChanged: (v) => setState(() => _environment = v),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _changeRequest,
                decoration: const InputDecoration(
                  labelText: 'Change request reference',
                  hintText: 'e.g. JIRA-1234',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _notes,
                maxLines: 2,
                decoration: const InputDecoration(
                  labelText: 'Deployment notes',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _rollbackPlan,
                maxLines: 2,
                decoration: const InputDecoration(
                  labelText: 'Rollback plan',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: _strategy,
                decoration: const InputDecoration(
                  labelText: 'Update strategy',
                  isDense: true,
                ),
                items: const [
                  DropdownMenuItem(
                    value: 'recreate',
                    child: Text('Recreate — replace everything at once'),
                  ),
                  DropdownMenuItem(
                    value: 'rolling',
                    child: Text(
                      'Rolling — one container at a time, auto-rollback',
                    ),
                  ),
                ],
                onChanged: (v) => setState(() => _strategy = v ?? 'recreate'),
              ),
              CheckboxListTile(
                contentPadding: EdgeInsets.zero,
                value: _autoRollback,
                onChanged: (v) => setState(() => _autoRollback = v ?? false),
                title: const Text('Roll back automatically if unhealthy'),
                subtitle: const Text(
                  'Redeploys the last healthy revision when post-deployment '
                  'health verification fails.',
                ),
              ),
              if (widget.isGitSource) ...[
                TextField(
                  controller: _gitRef,
                  decoration: const InputDecoration(
                    labelText: 'Git branch or tag to track (optional)',
                    hintText: 'Empty = the Compose file\'s own branch',
                    isDense: true,
                  ),
                ),
                CheckboxListTile(
                  contentPadding: EdgeInsets.zero,
                  value: _autoDeploy,
                  onChanged: (v) => setState(() => _autoDeploy = v ?? false),
                  title: const Text('Redeploy automatically on Git push'),
                ),
              ],
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(_error!, style: const TextStyle(color: AppColors.failed)),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _saving ? null : _save,
          child: _saving
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Save'),
        ),
      ],
    );
  }
}
