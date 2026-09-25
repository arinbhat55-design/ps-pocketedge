import 'package:flutter/material.dart';

import '../../models/database.dart';
import '../../theme/app_theme.dart';
import '../../widgets/state_message.dart' show isCompactWidth;
import '../../widgets/status_pill.dart';

/// A database's overall status from its deployment's phase and health.
StatusLabel databaseStatus(DatabaseInstance d) {
  if (d.phase == 'running' || d.phase == 'healthy') {
    return switch (d.healthStatus) {
      'healthy' => (label: 'Healthy', tone: StatusTone.healthy),
      'unhealthy' => (label: 'Unhealthy', tone: StatusTone.failed),
      'verifying' => (label: 'Starting', tone: StatusTone.warning),
      _ => (label: 'Running', tone: StatusTone.healthy),
    };
  }
  return switch (d.phase) {
    'pending' => (label: 'Pending', tone: StatusTone.neutral),
    'awaiting_approval' => (
      label: 'Awaiting approval',
      tone: StatusTone.warning,
    ),
    'scheduled' => (label: 'Scheduled', tone: StatusTone.neutral),
    'pulling' => (label: 'Pulling image', tone: StatusTone.warning),
    'creating' => (label: 'Creating', tone: StatusTone.warning),
    'verifying' => (label: 'Starting', tone: StatusTone.warning),
    'unhealthy' => (label: 'Unhealthy', tone: StatusTone.failed),
    'failed' => (label: 'Failed', tone: StatusTone.failed),
    'rolled_back' => (label: 'Rolled back', tone: StatusTone.failed),
    'stopped' || 'removed' => (label: 'Stopped', tone: StatusTone.neutral),
    _ => (label: d.phase, tone: StatusTone.neutral),
  };
}

IconData categoryIcon(String category) => switch (category) {
  'relational' => Icons.table_chart_outlined,
  'nosql' => Icons.account_tree_outlined,
  'cache' => Icons.bolt_outlined,
  'analytics' => Icons.insights_outlined,
  'vector' => Icons.scatter_plot_outlined,
  _ => Icons.storage_outlined,
};

/// A category-iconed square used to mark an engine.
class EngineAvatar extends StatelessWidget {
  final String category;

  const EngineAvatar({super.key, required this.category});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 40,
      height: 40,
      decoration: BoxDecoration(
        color: AppColors.tealContainer,
        borderRadius: BorderRadius.circular(Radii.md),
      ),
      child: Icon(
        categoryIcon(category),
        color: AppColors.onTealContainer,
        size: 22,
      ),
    );
  }
}

/// A small icon + label chip describing an engine trait.
class EngineBadge extends StatelessWidget {
  final IconData icon;
  final String label;
  final bool warning;

  const EngineBadge({
    super.key,
    required this.icon,
    required this.label,
    this.warning = false,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final color = warning
        ? AppColors.warning
        : theme.colorScheme.onSurfaceVariant;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(Radii.sm),
        border: Border.all(color: theme.colorScheme.outlineVariant),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 13, color: color),
          const SizedBox(width: 4),
          Text(
            label,
            style: theme.textTheme.labelSmall?.copyWith(color: color),
          ),
        ],
      ),
    );
  }
}

/// A titled card section used by the database detail screen.
class SectionCard extends StatelessWidget {
  final String title;
  final IconData icon;
  final List<Widget> actions;
  final Widget child;

  const SectionCard({
    super.key,
    required this.title,
    required this.icon,
    this.actions = const [],
    required this.child,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final compact = isCompactWidth(context);
    return Card(
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.all(Space.lg),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Icon(icon, size: 20, color: theme.colorScheme.primary),
                const SizedBox(width: Space.sm),
                Expanded(
                  child: Text(title, style: theme.textTheme.titleMedium),
                ),
                if (!compact) ...actions,
              ],
            ),
            // On phones the actions get their own line rather than
            // squeezing the title.
            if (compact && actions.isNotEmpty) ...[
              const SizedBox(height: Space.sm),
              Wrap(spacing: Space.xs, runSpacing: Space.xs, children: actions),
            ],
            const SizedBox(height: Space.md),
            child,
          ],
        ),
      ),
    );
  }
}

/// A label/value row with an optional trailing widget.
class KeyValueRow extends StatelessWidget {
  final String label;
  final String value;
  final bool monospace;
  final Widget? trailing;

  const KeyValueRow({
    super.key,
    required this.label,
    required this.value,
    this.monospace = false,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          SizedBox(
            width: 130,
            child: Text(
              label,
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: monospace
                  ? theme.textTheme.bodyMedium?.copyWith(
                      fontFamily: 'monospace',
                    )
                  : theme.textTheme.bodyMedium,
            ),
          ),
          ?trailing,
        ],
      ),
    );
  }
}

/// Backup schedule presets. Schedules are UTC on the server; the labels
/// say so.
const backupPresets = <String, String>{
  '': 'No scheduled backups',
  '0 * * * *': 'Hourly',
  '0 */6 * * *': 'Every 6 hours',
  '0 2 * * *': 'Daily at 02:00 UTC',
  '0 2 * * 0': 'Weekly, Sunday 02:00 UTC',
};

String describeBackupCron(String? cron) {
  if (cron == null || cron.isEmpty) return 'Not scheduled';
  return backupPresets[cron] ?? '$cron (UTC)';
}

String describeRetention(int days, int count) {
  final parts = [
    if (days > 0) '$days day${days == 1 ? '' : 's'}',
    if (count > 0) 'newest $count',
  ];
  return parts.isEmpty ? 'Keep all backups' : 'Keep ${parts.join(' and ')}';
}
