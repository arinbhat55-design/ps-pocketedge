import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/resource_insights.dart';
import '../../models/server.dart';
import '../../theme/app_theme.dart';
import '../../widgets/formatting.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';
import 'alert_rule_dialog.dart';

/// Fleet-wide container resource alerting: alerts raised by threshold
/// rules or by abnormal-usage detection (evaluated every minute by the
/// control plane, whether or not the app is open), and the rules
/// themselves.
class AlertsScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const AlertsScreen({super.key, required this.apiClient, this.isAdmin = true});

  @override
  State<AlertsScreen> createState() => _AlertsScreenState();
}

class _AlertsScreenState extends State<AlertsScreen> {
  List<ContainerAlert>? _alerts;
  List<ContainerAlertRule>? _rules;
  Map<String, String> _serverNames = {};
  Object? _error;
  bool _showResolved = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      final results = await Future.wait([
        widget.apiClient.listContainerAlerts(includeResolved: _showResolved),
        widget.apiClient.listContainerAlertRules(),
        widget.apiClient.listServers(),
      ]);
      if (!mounted) return;
      setState(() {
        _alerts = results[0] as List<ContainerAlert>;
        _rules = results[1] as List<ContainerAlertRule>;
        _serverNames = {
          for (final s in results[2] as List<Server>) s.id: s.name,
        };
      });
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  void _snack(String message) {
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _acknowledge(ContainerAlert alert) async {
    try {
      await widget.apiClient.acknowledgeContainerAlert(alert.id);
      _load();
    } catch (e) {
      _snack('Couldn\'t acknowledge: $e');
    }
  }

  Future<void> _editRule([ContainerAlertRule? rule]) async {
    final saved = await showAlertRuleDialog(
      context,
      apiClient: widget.apiClient,
      servers: _serverNames,
      rule: rule,
    );
    if (saved == true) _load();
  }

  Future<void> _toggleRule(ContainerAlertRule rule, bool enabled) async {
    try {
      await widget.apiClient.updateContainerAlertRule(
        rule.copyWith(enabled: enabled),
      );
      _load();
    } catch (e) {
      _snack('Couldn\'t update rule: $e');
    }
  }

  Future<void> _deleteRule(ContainerAlertRule rule) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Delete "${rule.name}"?'),
        content: const Text(
          'Its open alerts are resolved; past alerts stay in the history.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: AppColors.failed),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await widget.apiClient.deleteContainerAlertRule(rule.id);
      _load();
    } catch (e) {
      _snack('Couldn\'t delete rule: $e');
    }
  }

  @override
  Widget build(BuildContext context) {
    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('Alerts'),
          actions: [
            IconButton(
              tooltip: 'Refresh',
              onPressed: _load,
              icon: const Icon(Icons.refresh),
            ),
          ],
          bottom: const TabBar(
            tabs: [
              Tab(text: 'Alerts'),
              Tab(text: 'Rules'),
            ],
          ),
        ),
        floatingActionButton: widget.isAdmin
            ? FloatingActionButton.extended(
                onPressed: _rules == null ? null : () => _editRule(),
                icon: const Icon(Icons.add_alert_outlined),
                label: const Text('New rule'),
              )
            : null,
        body: _error != null && _alerts == null
            ? StateMessage.error(what: 'alerts', error: _error, onRetry: _load)
            : _alerts == null
            ? const Center(child: CircularProgressIndicator())
            : TabBarView(children: [_buildAlerts(), _buildRules()]),
      ),
    );
  }

  Widget _buildAlerts() {
    final alerts = _alerts!;
    final open = alerts.where((a) => a.isOpen).toList();
    final critical = open.where((a) => a.severity == 'critical').length;
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(Space.lg, Space.lg, Space.lg, 96),
        children: [
          PageIntro(
            description:
                'Raised when a container breaches one of your rules, or '
                'when its usage departs sharply from its own recent '
                'baseline (spikes, memory leaks). Checked every minute.',
            summary: [
              SummaryStat(
                value: '${open.length}',
                label: 'firing',
                color: open.isEmpty ? null : AppColors.warning,
              ),
              if (critical > 0)
                SummaryStat(
                  value: '$critical',
                  label: 'critical',
                  color: AppColors.failed,
                ),
            ],
            action: FilterChip(
              label: const Text('Show resolved'),
              selected: _showResolved,
              onSelected: (v) {
                setState(() => _showResolved = v);
                _load();
              },
            ),
          ),
          const SizedBox(height: Space.lg),
          if (alerts.isEmpty)
            // Inline rather than StateMessage, which brings its own
            // scroll view and can't sit inside this ListView.
            Padding(
              padding: const EdgeInsets.only(top: Space.xxl),
              child: Column(
                children: [
                  const Icon(
                    Icons.notifications_none,
                    size: 40,
                    color: AppColors.textMuted,
                  ),
                  const SizedBox(height: Space.md),
                  Text(
                    _showResolved ? 'No alerts yet' : 'No alerts firing',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: Space.xs),
                  Text(
                    'Every running container is within its rules and usual '
                    'usage.',
                    textAlign: TextAlign.center,
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
              ),
            ),
          for (final a in alerts)
            _AlertCard(
              alert: a,
              serverName: a.serverName.isNotEmpty
                  ? a.serverName
                  : (_serverNames[a.serverId] ?? a.serverId),
              onAcknowledge: widget.isAdmin && a.isOpen && !a.isAcknowledged
                  ? () => _acknowledge(a)
                  : null,
            ),
        ],
      ),
    );
  }

  Widget _buildRules() {
    final rules = _rules!;
    if (rules.isEmpty) {
      return StateMessage(
        icon: Icons.rule,
        title: 'No alert rules yet',
        message:
            'Add a rule like "CPU above 90% for 5 minutes" to be alerted when '
            'a container runs hot. Abnormal-usage detection works without '
            'any rules.',
        actionLabel: widget.isAdmin ? 'New rule' : null,
        actionIcon: widget.isAdmin ? Icons.add : null,
        onAction: widget.isAdmin ? () => _editRule() : null,
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.fromLTRB(0, Space.sm, 0, 96),
        itemCount: rules.length,
        separatorBuilder: (_, _) => const Divider(height: 1),
        itemBuilder: (context, i) {
          final r = rules[i];
          final scope = [
            r.serverId == null
                ? 'All servers'
                : (_serverNames[r.serverId] ?? 'Removed server'),
            r.containerName ?? 'all containers',
          ].join(' · ');
          return ListTile(
            onTap: widget.isAdmin ? () => _editRule(r) : null,
            leading: Icon(
              r.severity == 'critical'
                  ? Icons.error_outline
                  : Icons.warning_amber_outlined,
              color: r.severity == 'critical'
                  ? AppColors.failed
                  : AppColors.warning,
            ),
            title: Text(r.name),
            subtitle: Text('${r.conditionLabel}\n$scope'),
            isThreeLine: true,
            trailing: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (widget.isAdmin)
                  Switch(value: r.enabled, onChanged: (v) => _toggleRule(r, v)),
                if (widget.isAdmin)
                  IconButton(
                    tooltip: 'Delete rule',
                    onPressed: () => _deleteRule(r),
                    icon: const Icon(Icons.delete_outline),
                  ),
              ],
            ),
          );
        },
      ),
    );
  }
}

class _AlertCard extends StatelessWidget {
  final ContainerAlert alert;
  final String serverName;
  final VoidCallback? onAcknowledge;

  const _AlertCard({
    required this.alert,
    required this.serverName,
    this.onAcknowledge,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final a = alert;
    final StatusLabel status = !a.isOpen
        ? (label: 'Resolved', tone: StatusTone.healthy)
        : a.severity == 'critical'
        ? (label: 'Critical', tone: StatusTone.failed)
        : (label: 'Warning', tone: StatusTone.warning);
    final title = a.kind == 'anomaly'
        ? (a.anomalyKind == 'leak'
              ? 'Possible memory leak'
              : 'Abnormal ${metricLabel(a.metric).toLowerCase()} usage')
        : (a.ruleName ?? 'Deleted rule');
    final timing = a.isOpen
        ? 'Firing since ${formatAgo(a.startedAt)}'
        : 'Resolved ${formatAgo(a.resolvedAt!)} · lasted '
              '${_duration(a.resolvedAt!.difference(a.startedAt))}';

    return Card(
      margin: const EdgeInsets.only(bottom: Space.md),
      child: Padding(
        padding: const EdgeInsets.all(Space.md),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Wrap(
              spacing: Space.sm,
              runSpacing: Space.xs,
              crossAxisAlignment: WrapCrossAlignment.center,
              children: [
                StatusPill.of(status),
                Text(title, style: theme.textTheme.titleSmall),
                if (a.isAcknowledged && a.isOpen)
                  Text('· acknowledged', style: theme.textTheme.bodySmall),
              ],
            ),
            const SizedBox(height: Space.sm),
            Text(
              '${a.containerName} on $serverName',
              style: theme.textTheme.bodyMedium?.copyWith(
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: Space.xs),
            Text(a.message, style: theme.textTheme.bodySmall),
            const SizedBox(height: Space.sm),
            Row(
              children: [
                Expanded(child: Text(timing, style: theme.textTheme.bodySmall)),
                if (onAcknowledge != null)
                  TextButton(
                    onPressed: onAcknowledge,
                    child: const Text('Acknowledge'),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

String _duration(Duration d) {
  if (d.inMinutes < 1) return '${d.inSeconds}s';
  if (d.inHours < 1) return '${d.inMinutes} min';
  return '${d.inHours}h ${d.inMinutes % 60}m';
}
