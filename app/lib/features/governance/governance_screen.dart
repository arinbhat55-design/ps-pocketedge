import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/audit_event.dart';
import '../../models/deployment.dart';
import '../../models/deployment_request.dart';
import '../../models/environment_policy.dart';
import '../deployments/deployment_status_screen.dart';
import '../deployments/deployment_widgets.dart';
import '../../theme/app_theme.dart';

/// Deployment governance: the approval queue, per-environment policies
/// (approval, required change request/rollback plan, maintenance windows),
/// and the audit trail.
class GovernanceScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const GovernanceScreen({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
  });

  @override
  State<GovernanceScreen> createState() => _GovernanceScreenState();
}

enum _Section { approvals, policies, audit }

class _GovernanceScreenState extends State<GovernanceScreen> {
  _Section _section = _Section.approvals;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 8),
          child: Align(
            alignment: Alignment.centerLeft,
            child: SegmentedButton<_Section>(
              segments: [
                const ButtonSegment(
                  value: _Section.approvals,
                  icon: Icon(Icons.how_to_reg_outlined),
                  label: Text('Approvals'),
                ),
                const ButtonSegment(
                  value: _Section.policies,
                  icon: Icon(Icons.policy_outlined),
                  label: Text('Environments'),
                ),
                if (widget.isAdmin)
                  const ButtonSegment(
                    value: _Section.audit,
                    icon: Icon(Icons.receipt_long_outlined),
                    label: Text('Audit trail'),
                  ),
              ],
              selected: {_section},
              onSelectionChanged: (s) => setState(() => _section = s.first),
            ),
          ),
        ),
        const Divider(height: 1),
        Expanded(
          child: switch (_section) {
            _Section.approvals => ApprovalsView(
              apiClient: widget.apiClient,
              isAdmin: widget.isAdmin,
            ),
            _Section.policies => EnvironmentPoliciesView(
              apiClient: widget.apiClient,
              isAdmin: widget.isAdmin,
            ),
            _Section.audit => AuditTrailView(apiClient: widget.apiClient),
          },
        ),
      ],
    );
  }
}

/// "Approval workflow": requests waiting for approval or for a maintenance
/// window, plus recently decided ones.
class ApprovalsView extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const ApprovalsView({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
  });

  @override
  State<ApprovalsView> createState() => _ApprovalsViewState();
}

class _ApprovalsViewState extends State<ApprovalsView> {
  late Future<List<DeploymentRequest>> _future = widget.apiClient
      .listDeploymentRequests();
  bool _busy = false;

  void _refresh() => setState(() {
    _future = widget.apiClient.listDeploymentRequests();
  });

  Future<void> _run(Future<void> Function() call) async {
    setState(() => _busy = true);
    try {
      await call();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Failed: ${e is ApiException ? e.message : e}'),
          ),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _busy = false);
        _refresh();
      }
    }
  }

  Future<void> _approve(DeploymentRequest r) async {
    final comment = await promptComment(
      context,
      title: 'Approve: ${r.summary}',
      action: 'Approve',
    );
    if (comment == null || !mounted) return;
    await _run(() async {
      final outcome = await widget.apiClient.approveDeploymentRequest(
        r.id,
        comment: comment,
      );
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(outcome.describe())));
      }
    });
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
    await _run(
      () => widget.apiClient.rejectDeploymentRequest(r.id, comment: comment),
    );
  }

  Future<void> _cancel(DeploymentRequest r) async {
    final comment = await promptComment(
      context,
      title: 'Cancel: ${r.summary}',
      action: 'Cancel request',
      destructive: true,
    );
    if (comment == null || !mounted) return;
    await _run(
      () => widget.apiClient.cancelDeploymentRequest(r.id, comment: comment),
    );
  }

  void _open(DeploymentRequest r) {
    Navigator.of(context)
        .push(
          MaterialPageRoute(
            builder: (_) => DeploymentStatusScreen(
              apiClient: widget.apiClient,
              deploymentId: r.deploymentId,
              isAdmin: widget.isAdmin,
            ),
          ),
        )
        .then((_) => _refresh());
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<List<DeploymentRequest>>(
      future: _future,
      builder: (context, snapshot) {
        if (snapshot.connectionState != ConnectionState.done) {
          return const Center(child: CircularProgressIndicator());
        }
        if (snapshot.hasError) {
          return Center(
            child: Text('Failed to load requests: ${snapshot.error}'),
          );
        }
        final all = snapshot.data ?? const [];
        final open = all.where((r) => r.isOpen).toList();
        final closed = all.where((r) => !r.isOpen).take(50).toList();
        return RefreshIndicator(
          onRefresh: () async => _refresh(),
          child: ListView(
            children: [
              if (_busy) const LinearProgressIndicator(),
              _heading(context, 'Waiting (${open.length})'),
              if (open.isEmpty)
                const Padding(
                  padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  child: Text(
                    'Nothing is waiting for approval or a maintenance window.',
                  ),
                ),
              for (final r in open)
                _RequestTile(
                  request: r,
                  onTap: () => _open(r),
                  actions: [
                    TextButton(
                      onPressed: _busy ? null : () => _cancel(r),
                      child: const Text('Cancel'),
                    ),
                    if (r.status == 'pending_approval' && widget.isAdmin) ...[
                      TextButton(
                        onPressed: _busy ? null : () => _reject(r),
                        child: const Text('Reject'),
                      ),
                      FilledButton(
                        onPressed: _busy ? null : () => _approve(r),
                        child: const Text('Approve'),
                      ),
                    ],
                  ],
                ),
              _heading(context, 'Recently decided'),
              if (closed.isEmpty)
                const Padding(
                  padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  child: Text('No approvals have been decided yet.'),
                ),
              for (final r in closed)
                _RequestTile(request: r, onTap: () => _open(r)),
            ],
          ),
        );
      },
    );
  }

  Widget _heading(BuildContext context, String text) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
    child: Text(text, style: Theme.of(context).textTheme.titleSmall),
  );
}

class _RequestTile extends StatelessWidget {
  final DeploymentRequest request;
  final VoidCallback onTap;
  final List<Widget> actions;

  const _RequestTile({
    required this.request,
    required this.onTap,
    this.actions = const [],
  });

  @override
  Widget build(BuildContext context) {
    final r = request;
    final lines = <String>[
      [
        humanizePhase(r.status),
        if (r.environment != null) r.environment!,
        if (r.serverName.isNotEmpty) r.serverName,
        if (r.changeRequest.isNotEmpty) r.changeRequest,
      ].join(' • '),
      [
        'requested by ${r.requestedByEmail ?? 'system'} ${formatTimestamp(r.requestedAt)}',
        if (r.scheduledFor != null && r.status == 'scheduled')
          'runs ${formatTimestamp(r.scheduledFor!)}',
        if (r.decidedByEmail != null)
          '${r.status == 'rejected' ? 'rejected' : 'decided'} by ${r.decidedByEmail}',
      ].join(' • '),
      if (r.decisionComment.isNotEmpty) '“${r.decisionComment}”',
      if (r.resultMessage.isNotEmpty) r.resultMessage,
    ];
    return ListTile(
      onTap: onTap,
      leading: Icon(Icons.circle, size: 12, color: phaseColor(r.status)),
      title: Text('${r.summary} — ${r.sourceName}'),
      subtitle: Text(lines.join('\n')),
      isThreeLine: lines.length > 2,
      trailing: actions.isEmpty
          ? null
          : Row(mainAxisSize: MainAxisSize.min, children: actions),
    );
  }
}

/// "Development, test, and production environments" + "Maintenance
/// window": each environment's governance policy.
class EnvironmentPoliciesView extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const EnvironmentPoliciesView({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
  });

  @override
  State<EnvironmentPoliciesView> createState() =>
      _EnvironmentPoliciesViewState();
}

class _EnvironmentPoliciesViewState extends State<EnvironmentPoliciesView> {
  late Future<List<EnvironmentPolicy>> _future = widget.apiClient
      .listEnvironmentPolicies();

  void _refresh() => setState(() {
    _future = widget.apiClient.listEnvironmentPolicies();
  });

  Future<void> _edit(EnvironmentPolicy policy) async {
    final saved = await showDialog<bool>(
      context: context,
      builder: (_) =>
          _PolicyDialog(apiClient: widget.apiClient, existing: policy),
    );
    if (saved == true) _refresh();
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<List<EnvironmentPolicy>>(
      future: _future,
      builder: (context, snapshot) {
        if (snapshot.connectionState != ConnectionState.done) {
          return const Center(child: CircularProgressIndicator());
        }
        if (snapshot.hasError) {
          return Center(
            child: Text('Failed to load policies: ${snapshot.error}'),
          );
        }
        final policies = snapshot.data ?? const [];
        return ListView(
          padding: const EdgeInsets.all(12),
          children: [
            for (final p in policies)
              Card(
                child: ListTile(
                  title: Text(
                    p.environment[0].toUpperCase() + p.environment.substring(1),
                  ),
                  subtitle: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const SizedBox(height: 4),
                      Wrap(
                        spacing: 6,
                        runSpacing: 4,
                        children: [
                          if (p.ruleLabels.isEmpty)
                            const Chip(
                              visualDensity: VisualDensity.compact,
                              label: Text('No restrictions'),
                            ),
                          for (final l in p.ruleLabels)
                            Chip(
                              visualDensity: VisualDensity.compact,
                              label: Text(l),
                            ),
                        ],
                      ),
                      for (final w in p.maintenanceWindows)
                        Text('Window: ${w.describe()}'),
                      if (p.enforceMaintenanceWindow)
                        Text(
                          p.inMaintenanceWindow
                              ? 'Inside a maintenance window now'
                              : p.nextWindow == null
                              ? 'No upcoming window'
                              : 'Next window: ${formatTimestamp(p.nextWindow!)}',
                          style: TextStyle(
                            color: p.inMaintenanceWindow ? AppColors.healthy : null,
                          ),
                        ),
                    ],
                  ),
                  trailing: widget.isAdmin
                      ? IconButton(
                          icon: const Icon(Icons.edit_outlined),
                          tooltip: 'Edit policy',
                          onPressed: () => _edit(p),
                        )
                      : null,
                ),
              ),
            if (!widget.isAdmin)
              const Padding(
                padding: EdgeInsets.all(8),
                child: Text('Only admins can change environment policies.'),
              ),
          ],
        );
      },
    );
  }
}

class _PolicyDialog extends StatefulWidget {
  final ApiClient apiClient;
  final EnvironmentPolicy existing;

  const _PolicyDialog({required this.apiClient, required this.existing});

  @override
  State<_PolicyDialog> createState() => _PolicyDialogState();
}

class _PolicyDialogState extends State<_PolicyDialog> {
  late bool _requireApproval = widget.existing.requireApproval;
  late bool _allowSelfApproval = widget.existing.allowSelfApproval;
  late bool _requireChangeRequest = widget.existing.requireChangeRequest;
  late bool _requireRollbackPlan = widget.existing.requireRollbackPlan;
  late bool _enforceWindow = widget.existing.enforceMaintenanceWindow;
  late final List<_WindowDraft> _windows = [
    for (final w in widget.existing.maintenanceWindows) _WindowDraft.from(w),
  ];
  bool _saving = false;
  String? _error;

  @override
  void dispose() {
    for (final w in _windows) {
      w.dispose();
    }
    super.dispose();
  }

  Future<void> _save() async {
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      await widget.apiClient.updateEnvironmentPolicy(
        EnvironmentPolicy(
          environment: widget.existing.environment,
          requireApproval: _requireApproval,
          allowSelfApproval: _allowSelfApproval,
          requireChangeRequest: _requireChangeRequest,
          requireRollbackPlan: _requireRollbackPlan,
          enforceMaintenanceWindow: _enforceWindow,
          maintenanceWindows: [for (final w in _windows) w.toWindow()],
        ),
      );
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() => _error = e is ApiException ? e.message : '$e');
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(
        '${widget.existing.environment[0].toUpperCase()}'
        '${widget.existing.environment.substring(1)} policy',
      ),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                value: _requireApproval,
                onChanged: (v) => setState(() => _requireApproval = v),
                title: const Text('Require approval'),
                subtitle: const Text(
                  'Deploys, redeploys, rollbacks, and scaling wait for an admin to approve.',
                ),
              ),
              if (_requireApproval)
                SwitchListTile(
                  contentPadding: const EdgeInsets.only(left: 16),
                  value: _allowSelfApproval,
                  onChanged: (v) => setState(() => _allowSelfApproval = v),
                  title: const Text('Requesters may approve their own changes'),
                ),
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                value: _requireChangeRequest,
                onChanged: (v) => setState(() => _requireChangeRequest = v),
                title: const Text('Require a change request reference'),
              ),
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                value: _requireRollbackPlan,
                onChanged: (v) => setState(() => _requireRollbackPlan = v),
                title: const Text('Require a rollback plan'),
              ),
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                value: _enforceWindow,
                onChanged: (v) => setState(() => _enforceWindow = v),
                title: const Text('Only allow changes in maintenance windows'),
                subtitle: const Text(
                  'Outside a window, changes can be scheduled for the next one '
                  '(or run anyway by an admin override).',
                ),
              ),
              const SizedBox(height: 8),
              Text(
                'Maintenance windows (UTC)',
                style: Theme.of(context).textTheme.titleSmall,
              ),
              for (final w in _windows)
                _WindowEditor(
                  draft: w,
                  onChanged: () => setState(() {}),
                  onRemove: () => setState(() {
                    _windows.remove(w);
                    w.dispose();
                  }),
                ),
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: () => setState(
                    () => _windows.add(
                      _WindowDraft.from(
                        const MaintenanceWindow(
                          days: [6, 0],
                          start: '02:00',
                          end: '04:00',
                        ),
                      ),
                    ),
                  ),
                  icon: const Icon(Icons.add),
                  label: const Text('Add window'),
                ),
              ),
              if (_error != null)
                Text(_error!, style: const TextStyle(color: AppColors.failed)),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _saving ? null : _save,
          child: const Text('Save'),
        ),
      ],
    );
  }
}

class _WindowDraft {
  final Set<int> days;
  final TextEditingController start;
  final TextEditingController end;

  _WindowDraft(this.days, this.start, this.end);

  factory _WindowDraft.from(MaintenanceWindow w) => _WindowDraft(
    {...w.days},
    TextEditingController(text: w.start),
    TextEditingController(text: w.end),
  );

  MaintenanceWindow toWindow() => MaintenanceWindow(
    days: days.toList()..sort(),
    start: start.text.trim(),
    end: end.text.trim(),
  );

  void dispose() {
    start.dispose();
    end.dispose();
  }
}

/// One maintenance window: the weekdays on one line, then its UTC time
/// range (picked with a time picker, so it's always valid HH:MM).
class _WindowEditor extends StatelessWidget {
  final _WindowDraft draft;
  final VoidCallback onChanged;
  final VoidCallback onRemove;

  const _WindowEditor({
    required this.draft,
    required this.onChanged,
    required this.onRemove,
  });

  Future<void> _pick(BuildContext context, TextEditingController c) async {
    final parts = c.text.split(':');
    final initial = TimeOfDay(
      hour: int.tryParse(parts.first) ?? 0,
      minute: parts.length > 1 ? int.tryParse(parts[1]) ?? 0 : 0,
    );
    final picked = await showTimePicker(
      context: context,
      initialTime: initial,
      helpText: 'Time in UTC',
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(context).copyWith(alwaysUse24HourFormat: true),
        child: child!,
      ),
    );
    if (picked == null) return;
    String two(int n) => n.toString().padLeft(2, '0');
    c.text = '${two(picked.hour)}:${two(picked.minute)}';
    onChanged();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    Widget timeField(String label, TextEditingController c) => SizedBox(
      width: 110,
      child: TextField(
        controller: c,
        readOnly: true,
        onTap: () => _pick(context, c),
        decoration: InputDecoration(
          labelText: label,
          isDense: true,
          border: const OutlineInputBorder(),
          suffixIcon: const Icon(Icons.schedule, size: 18),
        ),
      ),
    );
    final start = draft.start.text;
    final end = draft.end.text;
    final wraps = end.compareTo(start) <= 0;
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.fromLTRB(12, 8, 4, 12),
      decoration: BoxDecoration(
        border: Border.all(color: theme.colorScheme.outlineVariant),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Wrap(
                  spacing: 4,
                  runSpacing: 4,
                  children: [
                    for (var d = 0; d < 7; d++)
                      FilterChip(
                        visualDensity: VisualDensity.compact,
                        showCheckmark: false,
                        label: Text(kWeekdays[d]),
                        selected: draft.days.contains(d),
                        onSelected: (v) {
                          v ? draft.days.add(d) : draft.days.remove(d);
                          onChanged();
                        },
                      ),
                  ],
                ),
              ),
              IconButton(
                icon: const Icon(Icons.delete_outline),
                tooltip: 'Remove window',
                onPressed: onRemove,
              ),
            ],
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              timeField('From', draft.start),
              const Padding(
                padding: EdgeInsets.symmetric(horizontal: 8),
                child: Icon(Icons.arrow_forward, size: 18),
              ),
              timeField('To', draft.end),
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  wraps ? 'UTC, ends the next day' : 'UTC',
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

/// "Complete audit trail" (admin): every user-initiated change, newest
/// first, filterable and paged.
class AuditTrailView extends StatefulWidget {
  final ApiClient apiClient;

  const AuditTrailView({super.key, required this.apiClient});

  @override
  State<AuditTrailView> createState() => _AuditTrailViewState();
}

class _AuditTrailViewState extends State<AuditTrailView> {
  static const _types = {
    'deployment': 'Deployments',
    'compose_file': 'Compose files',
    'env_var_group': 'Variable groups',
    'environment': 'Environment policies',
    'git_repository': 'Git repositories',
  };

  final List<AuditEvent> _events = [];
  final _search = TextEditingController();
  String? _entityType;
  bool _loading = false;
  bool _exhausted = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _reload();
  }

  @override
  void dispose() {
    _search.dispose();
    super.dispose();
  }

  Future<void> _reload() async {
    setState(() {
      _events.clear();
      _exhausted = false;
    });
    await _loadMore();
  }

  Future<void> _loadMore() async {
    if (_loading || _exhausted) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final page = await widget.apiClient.listAuditEvents(
        entityType: _entityType,
        search: _search.text.trim(),
        before: _events.isEmpty ? null : _events.last.id,
        limit: 100,
      );
      setState(() {
        _events.addAll(page);
        _exhausted = page.length < 100;
      });
    } catch (e) {
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  IconData _icon(String entityType) => switch (entityType) {
    'deployment' => Icons.rocket_launch_outlined,
    'compose_file' => Icons.layers_outlined,
    'env_var_group' => Icons.tune,
    'environment' => Icons.policy_outlined,
    'git_repository' => Icons.source_outlined,
    _ => Icons.history,
  };

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
                hint: 'Search audit trail',
                onSubmitted: (_) => _reload(),
              ),
              FilterDropdown<String>(
                value: _entityType,
                allLabel: 'Everything',
                options: _types,
                width: 210,
                onChanged: (v) {
                  _entityType = v;
                  _reload();
                },
              ),
              IconButton(
                icon: const Icon(Icons.refresh),
                onPressed: _reload,
                tooltip: 'Refresh',
              ),
            ],
          ),
        ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.all(16),
            child: Text(_error!, style: const TextStyle(color: AppColors.failed)),
          ),
        Expanded(
          child: NotificationListener<ScrollNotification>(
            onNotification: (n) {
              if (n.metrics.extentAfter < 300) _loadMore();
              return false;
            },
            child: ListView.builder(
              itemCount: _events.length + (_loading ? 1 : 0),
              itemBuilder: (context, i) {
                if (i >= _events.length) {
                  return const Padding(
                    padding: EdgeInsets.all(16),
                    child: Center(child: CircularProgressIndicator()),
                  );
                }
                final e = _events[i];
                return ListTile(
                  dense: true,
                  leading: Icon(_icon(e.entityType), size: 20),
                  title: Text(e.summary.isEmpty ? e.action : e.summary),
                  subtitle: Text(
                    '${e.action} • ${e.actor} • ${formatTimestamp(e.createdAt)}',
                  ),
                );
              },
            ),
          ),
        ),
      ],
    );
  }
}
