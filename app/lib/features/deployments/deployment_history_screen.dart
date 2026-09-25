import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/deployment.dart';
import '../../models/env_var_group.dart' show kEnvironments;
import 'deployment_status_screen.dart';
import 'deployment_widgets.dart';

/// "Deployment history": every deployment across servers, newest first,
/// filterable by environment/phase/text, with its health, current revision,
/// Git commit, and whether anything is waiting on approval. Tapping one
/// opens its [DeploymentStatusScreen].
class DeploymentHistoryScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const DeploymentHistoryScreen({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
  });

  @override
  State<DeploymentHistoryScreen> createState() =>
      _DeploymentHistoryScreenState();
}

class _DeploymentHistoryScreenState extends State<DeploymentHistoryScreen> {
  static const _phases = [
    'running',
    'failed',
    'awaiting_approval',
    'scheduled',
    'stopped',
    'pending',
  ];

  String? _environment;
  String? _phase;
  bool _includeRemoved = false;
  final _search = TextEditingController();
  late Future<List<DeploymentSummary>> _future;

  @override
  void initState() {
    super.initState();
    _future = _load();
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  Future<List<DeploymentSummary>> _load() => widget.apiClient.listDeployments(
    environment: _environment,
    phase: _phase,
    search: _search.text.trim(),
    includeRemoved: _includeRemoved,
  );

  void _refresh() => setState(() {
    _future = _load();
  });

  Future<void> _open(DeploymentSummary s) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => DeploymentStatusScreen(
          apiClient: widget.apiClient,
          deploymentId: s.deployment.id,
          composeFileId: s.deployment.composeFileId,
          isAdmin: widget.isAdmin,
        ),
      ),
    );
    _refresh();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
          child: Wrap(
            spacing: 12,
            runSpacing: 8,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              FilterSearchField(
                controller: _search,
                hint: 'Search deployments',
                onSubmitted: (_) => _refresh(),
              ),
              FilterDropdown<String>(
                value: _environment,
                allLabel: 'All environments',
                options: {for (final e in kEnvironments) e: humanizePhase(e)},
                onChanged: (v) {
                  _environment = v;
                  _refresh();
                },
              ),
              FilterDropdown<String>(
                value: _phase,
                allLabel: 'Any status',
                options: {for (final p in _phases) p: humanizePhase(p)},
                onChanged: (v) {
                  _phase = v;
                  _refresh();
                },
              ),
              SizedBox(
                height: kFilterControlHeight,
                child: FilterChip(
                  label: const Text('Include removed'),
                  selected: _includeRemoved,
                  onSelected: (v) {
                    _includeRemoved = v;
                    _refresh();
                  },
                ),
              ),
              IconButton(
                icon: const Icon(Icons.refresh),
                tooltip: 'Refresh',
                onPressed: _refresh,
              ),
            ],
          ),
        ),
        Expanded(
          child: FutureBuilder<List<DeploymentSummary>>(
            future: _future,
            builder: (context, snapshot) {
              if (snapshot.connectionState != ConnectionState.done) {
                return const Center(child: CircularProgressIndicator());
              }
              if (snapshot.hasError) {
                return Center(
                  child: Text('Failed to load deployments: ${snapshot.error}'),
                );
              }
              final list = snapshot.data ?? const [];
              if (list.isEmpty) {
                return const Center(
                  child: Text(
                    'No deployments match.\nDeploy a Compose file from the '
                    'Compose files tab.',
                    textAlign: TextAlign.center,
                  ),
                );
              }
              return RefreshIndicator(
                onRefresh: () async => _refresh(),
                child: ListView.separated(
                  itemCount: list.length,
                  separatorBuilder: (_, _) => const Divider(height: 1),
                  itemBuilder: (context, i) => _DeploymentRow(
                    summary: list[i],
                    onTap: () => _open(list[i]),
                  ),
                ),
              );
            },
          ),
        ),
      ],
    );
  }
}

class _DeploymentRow extends StatelessWidget {
  final DeploymentSummary summary;
  final VoidCallback onTap;

  const _DeploymentRow({required this.summary, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final d = summary.deployment;
    final small = Theme.of(context).textTheme.bodySmall;
    return ListTile(
      onTap: onTap,
      title: Row(
        children: [
          Flexible(
            child: Text(
              summary.sourceName.isEmpty ? d.id : summary.sourceName,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          const SizedBox(width: 8),
          StatusDot(status: d.phase),
          if (d.deployEnvironment != null) ...[
            const SizedBox(width: 8),
            _Tag(d.deployEnvironment!),
          ],
          if (summary.pendingRequests > 0) ...[
            const SizedBox(width: 8),
            _Tag(
              '${summary.pendingRequests} waiting',
              color: phaseColor('pending_approval'),
            ),
          ],
        ],
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'On '
            '${summary.serverName.isEmpty ? d.serverId : summary.serverName}',
            style: small,
          ),
          Text(
            [
              if (d.currentRevision > 0) 'Revision ${d.currentRevision}',
              if (summary.latestGitCommit.isNotEmpty)
                'commit ${shortCommit(summary.latestGitCommit)}',
              if (d.changeRequest.isNotEmpty) d.changeRequest,
              'by ${summary.createdByEmail ?? 'unknown'}',
              formatTimestamp(d.createdAt),
            ].join(' • '),
            style: small?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
            overflow: TextOverflow.ellipsis,
          ),
        ],
      ),
      isThreeLine: true,
      // Every row gets a right-hand badge so the column lines up: health
      // once a revision has run, otherwise the deployment's status.
      trailing: d.currentRevision > 0
          ? HealthBadge(status: d.healthStatus, message: d.healthMessage)
          : StatusChip(status: d.phase),
    );
  }
}

class _Tag extends StatelessWidget {
  final String text;
  final Color? color;

  const _Tag(this.text, {this.color});

  @override
  Widget build(BuildContext context) {
    final c = color ?? Theme.of(context).colorScheme.primary;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: c.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(text, style: TextStyle(fontSize: 11, color: c)),
    );
  }
}
