import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import '../../models/image.dart' show formatBytes;
import '../../models/resource_insights.dart';
import '../../theme/app_theme.dart';
import '../../widgets/formatting.dart';
import '../../widgets/notice_banner.dart';
import '../../widgets/state_message.dart' show describeLoadError;
import '../../widgets/status_pill.dart';

const _nanoCpusPerCore = 1000000000;

/// Resource insights for one container, shown under its live charts:
/// firing alerts, abnormal usage (spikes, memory leaks), and right-sizing
/// recommendations with a one-tap "review and apply". Everything is
/// computed server-side from the recorded metric history — see the control
/// plane's insights package.
class ContainerInsightsPanel extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;

  /// The container's inspected detail, for its current limits. The panel
  /// reloads when this changes (e.g. after limits are edited).
  final Future<ContainerDetail>? detailFuture;

  /// Opens the limits editor prefilled with [suggested]; resolves true
  /// once the new limits were applied.
  final Future<bool> Function(ResourceLimits suggested)? onReviewLimits;

  const ContainerInsightsPanel({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    this.detailFuture,
    this.onReviewLimits,
  });

  @override
  State<ContainerInsightsPanel> createState() => _ContainerInsightsPanelState();
}

class _ContainerInsightsPanelState extends State<ContainerInsightsPanel> {
  ContainerInsights? _insights;
  Object? _error;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(ContainerInsightsPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.detailFuture != widget.detailFuture) _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    ResourceLimits? limits;
    try {
      final detail = await widget.detailFuture;
      if (detail != null) {
        limits = ResourceLimits(
          nanoCpus: detail.nanoCpus,
          memoryLimitBytes: detail.memoryLimitBytes,
          memoryReservationBytes: detail.memoryReservationBytes,
          pidsLimit: detail.pidsLimit,
        );
      }
    } catch (_) {
      // Without the current limits, recommendations treat every limit as
      // unset — still useful, so carry on.
    }
    try {
      final insights = await widget.apiClient.getContainerInsights(
        widget.serverId,
        widget.containerId,
        limits: limits,
      );
      if (mounted) setState(() => _insights = insights);
    } catch (e) {
      if (mounted) setState(() => _error = e);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  // On success the owner refreshes detailFuture, which reloads the panel
  // via didUpdateWidget with the new limits.
  Future<void> _review(ResourceLimits suggested) async {
    await widget.onReviewLimits?.call(suggested);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final insights = _insights;

    final header = Row(
      children: [
        Expanded(child: Text('Insights', style: theme.textTheme.titleMedium)),
        IconButton(
          tooltip: 'Refresh insights',
          onPressed: _loading ? null : _load,
          icon: _loading
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.refresh, size: 20),
        ),
      ],
    );

    if (insights == null) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          header,
          if (_error != null)
            Text(
              'Couldn\'t load insights: ${describeLoadError(_error)}',
              style: const TextStyle(color: AppColors.failed),
            ),
        ],
      );
    }

    final stats = insights.stats;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        header,
        if (stats != null)
          Text(
            'Based on ${stats.sampleCount} samples since '
            '${formatAgo(stats.from)}.',
            style: theme.textTheme.bodySmall,
          ),
        const SizedBox(height: Space.md),
        for (final alert in insights.openAlerts) ...[
          NoticeBanner(
            tone: _tone(alert.severity),
            icon: Icons.notifications_active_outlined,
            title: alert.kind == 'anomaly'
                ? 'Abnormal ${metricLabel(alert.metric).toLowerCase()} usage'
                : (alert.ruleName ?? 'Alert rule breached'),
            message:
                '${alert.message} Firing since ${formatAgo(alert.startedAt)}.',
          ),
          const SizedBox(height: Space.sm),
        ],
        _Section(
          title: 'Abnormal usage',
          icon: Icons.insights_outlined,
          empty: stats == null
              ? 'No usage history yet.'
              : 'Nothing unusual — usage is in line with its recent baseline.',
          children: [
            for (final a in insights.anomalies)
              _FindingTile(
                severity: a.severity,
                title: a.kind == 'leak'
                    ? 'Possible memory leak'
                    : '${metricLabel(a.metric)} spike',
                message: a.message,
              ),
          ],
        ),
        const SizedBox(height: Space.lg),
        _Section(
          title: 'Recommended limits',
          icon: Icons.tune,
          empty: stats == null || stats.sampleCount < 90
              ? 'Recommendations need about 30 minutes of usage history.'
              : 'Current limits fit observed usage well.',
          action:
              insights.recommendations.isEmpty || widget.onReviewLimits == null
              ? null
              : FilledButton.tonalIcon(
                  onPressed: () => _review(insights.suggestedLimits),
                  icon: const Icon(Icons.auto_fix_high, size: 18),
                  label: const Text('Review & apply'),
                ),
          children: [
            for (final r in insights.recommendations)
              _FindingTile(
                severity: r.severity,
                title:
                    '${_resourceLabel(r.resource)}: '
                    '${_formatLimit(r.resource, r.current)} → '
                    '${_formatLimit(r.resource, r.suggested)}',
                message: r.message,
              ),
          ],
        ),
      ],
    );
  }
}

StatusTone _tone(String severity) => switch (severity) {
  'critical' => StatusTone.failed,
  'warning' => StatusTone.warning,
  _ => StatusTone.neutral,
};

String _resourceLabel(String resource) => switch (resource) {
  'cpu' => 'CPU limit',
  'memory' => 'Memory limit',
  'memoryReservation' => 'Memory reservation',
  'pids' => 'Process limit',
  _ => resource,
};

String _formatLimit(String resource, int value) {
  if (value <= 0) return resource == 'memoryReservation' ? 'none' : 'unlimited';
  return switch (resource) {
    'cpu' => '${(value / _nanoCpusPerCore).toStringAsFixed(2)} cores',
    'memory' || 'memoryReservation' => formatBytes(value),
    _ => '$value',
  };
}

class _Section extends StatelessWidget {
  final String title;
  final IconData icon;
  final String empty;
  final Widget? action;
  final List<Widget> children;

  const _Section({
    required this.title,
    required this.icon,
    required this.empty,
    this.action,
    required this.children,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(Space.md),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Icon(icon, size: 18, color: theme.colorScheme.primary),
                const SizedBox(width: Space.sm),
                Expanded(child: Text(title, style: theme.textTheme.titleSmall)),
                ?action,
              ],
            ),
            const SizedBox(height: Space.sm),
            if (children.isEmpty)
              Text(empty, style: theme.textTheme.bodySmall)
            else
              ...children,
          ],
        ),
      ),
    );
  }
}

class _FindingTile extends StatelessWidget {
  final String severity;
  final String title;
  final String message;

  const _FindingTile({
    required this.severity,
    required this.title,
    required this.message,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final tone = _tone(severity);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: Space.sm),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(
            spacing: Space.sm,
            runSpacing: Space.xs,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              StatusPill.of((
                label: severity[0].toUpperCase() + severity.substring(1),
                tone: tone,
              )),
              Text(
                title,
                style: theme.textTheme.bodyMedium?.copyWith(
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
          const SizedBox(height: Space.xs),
          Text(message, style: theme.textTheme.bodySmall),
        ],
      ),
    );
  }
}
