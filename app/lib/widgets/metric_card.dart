import 'dart:math' as math;

import 'package:fl_chart/fl_chart.dart';
import 'package:flutter/material.dart';

import '../theme/app_theme.dart';
import 'formatting.dart';
import 'status_pill.dart';

class MetricPoint {
  final DateTime at;
  final double value;

  const MetricPoint(this.at, this.value);
}

/// One resource metric (CPU, memory, disk; always a percentage): the
/// current value large, a one-line summary for the visible range, and a
/// quiet sparkline underneath with the range labelled on its time axis.
///
/// The line is neutral while the value is normal and takes the warning or
/// failed color only when it's high. When [stale] (live updates
/// disconnected), everything dims and the card says how old the value is,
/// so a frozen number is never mistaken for a live one.
class MetricCard extends StatelessWidget {
  final String label;
  final IconData icon;
  final List<MetricPoint> points;
  final Duration range;
  final DateTime now;
  final bool stale;

  const MetricCard({
    super.key,
    required this.label,
    required this.icon,
    required this.points,
    required this.range,
    required this.now,
    this.stale = false,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final start = now.subtract(range);
    final visible = [
      for (final p in points)
        if (!p.at.isBefore(start)) p,
    ];
    final latest = points.isEmpty ? null : points.last;
    final tone = latest == null ? null : usageTone(latest.value);
    final alert = !stale && tone != null && tone != StatusTone.healthy;

    final valueColor = stale
        ? AppColors.textMuted
        : theme.colorScheme.onSurface;
    final lineColor = stale
        ? AppColors.neutral.withValues(alpha: 0.6)
        : alert
        ? tone.color
        : AppColors.chartLine;

    String caption;
    if (latest == null) {
      caption = 'No data yet';
    } else if (stale) {
      caption = 'Last known value · ${formatAgo(latest.at, now: now)}';
    } else if (visible.isEmpty) {
      caption = 'No samples in the last ${formatRange(range)}';
    } else {
      final values = visible.map((p) => p.value);
      final avg = values.reduce((a, b) => a + b) / visible.length;
      final peak = values.reduce(math.max);
      caption =
          'Avg ${avg.toStringAsFixed(0)}% · '
          'peak ${peak.toStringAsFixed(0)}% · last ${formatRange(range)}';
    }

    return Card(
      child: Padding(
        padding: const EdgeInsets.all(Space.lg),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(icon, size: 16, color: theme.colorScheme.onSurfaceVariant),
                const SizedBox(width: Space.sm),
                Expanded(
                  child: Text(
                    label,
                    style: theme.textTheme.labelLarge?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                ),
                if (stale)
                  const StatusPill(label: 'Stale', tone: StatusTone.neutral)
                else if (alert)
                  StatusPill(
                    label: tone == StatusTone.failed ? 'Critical' : 'High',
                    tone: tone,
                  ),
              ],
            ),
            const SizedBox(height: Space.sm),
            Row(
              crossAxisAlignment: CrossAxisAlignment.baseline,
              textBaseline: TextBaseline.alphabetic,
              children: [
                Text(
                  latest == null ? '—' : latest.value.toStringAsFixed(0),
                  style: theme.textTheme.headlineMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                    color: valueColor,
                    fontFeatures: const [FontFeature.tabularFigures()],
                  ),
                ),
                if (latest != null)
                  Text(
                    '%',
                    style: theme.textTheme.titleMedium?.copyWith(
                      color: AppColors.textMuted,
                    ),
                  ),
              ],
            ),
            Text(caption, style: theme.textTheme.bodySmall),
            const SizedBox(height: Space.md),
            SizedBox(
              height: 56,
              child: visible.length < 2
                  ? Center(
                      child: Text(
                        stale ? 'Disconnected' : 'Waiting for data…',
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: AppColors.textMuted,
                        ),
                      ),
                    )
                  : _Sparkline(
                      points: visible,
                      start: start,
                      range: range,
                      color: lineColor,
                    ),
            ),
            const SizedBox(height: Space.xs),
            DefaultTextStyle.merge(
              style: theme.textTheme.labelSmall?.copyWith(
                color: AppColors.textMuted,
              ),
              child: Row(
                children: [
                  Text('${formatRange(range)} ago'),
                  const Spacer(),
                  Text(stale ? 'disconnected' : 'now'),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Sparkline extends StatelessWidget {
  final List<MetricPoint> points;
  final DateTime start;
  final Duration range;
  final Color color;

  const _Sparkline({
    required this.points,
    required this.start,
    required this.range,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    final grid = Theme.of(context).colorScheme.outlineVariant;
    return LineChart(
      LineChartData(
        // Plotted against time over the whole range, so gaps (agent
        // offline) show as gaps instead of being squeezed together.
        minX: 0,
        maxX: range.inSeconds.toDouble(),
        minY: 0,
        maxY: 100,
        gridData: FlGridData(
          drawVerticalLine: false,
          horizontalInterval: 50,
          getDrawingHorizontalLine: (_) =>
              FlLine(color: grid, strokeWidth: 1, dashArray: const [3, 4]),
        ),
        titlesData: const FlTitlesData(show: false),
        borderData: FlBorderData(show: false),
        lineTouchData: const LineTouchData(enabled: false),
        lineBarsData: [
          LineChartBarData(
            spots: [
              for (final p in points)
                FlSpot(
                  p.at.difference(start).inSeconds.toDouble(),
                  p.value.clamp(0, 100).toDouble(),
                ),
            ],
            isCurved: true,
            preventCurveOverShooting: true,
            color: color,
            barWidth: 1.5,
            dotData: const FlDotData(show: false),
            belowBarData: BarAreaData(
              show: true,
              color: color.withValues(alpha: 0.05),
            ),
          ),
        ],
      ),
    );
  }
}
