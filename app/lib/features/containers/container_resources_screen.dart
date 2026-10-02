import 'dart:async';
import 'dart:convert';

import 'package:fl_chart/fl_chart.dart';
import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import '../../models/image.dart' show formatBytes;
import '../../models/resource_insights.dart' show ResourceLimits, formatCores;
import '../../models/server_metrics.dart';
import '../../theme/app_theme.dart';
import 'container_insights_panel.dart';

/// Real-time and historical CPU/memory/network/storage consumption for one
/// container. Live values ride the same per-server WebSocket stream the
/// server detail screen already uses (filtered to this container's id)
/// rather than opening a dedicated connection — see the control plane's
/// grpcserver/session.go for how container_stats piggybacks on the
/// existing heartbeat push.
class ContainerResourcesScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String containerState;
  // For the insights panel: current limits, and the limits editor.
  final Future<ContainerDetail>? detailFuture;
  final Future<bool> Function(ResourceLimits suggested)? onReviewLimits;

  const ContainerResourcesScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.containerState,
    this.detailFuture,
    this.onReviewLimits,
  });

  @override
  State<ContainerResourcesScreen> createState() =>
      _ContainerResourcesScreenState();
}

class _ContainerResourcesScreenState extends State<ContainerResourcesScreen> {
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;

  final List<double> _cpuHistory = [];
  final List<double> _memHistory = [];
  ContainerResourceUsage? _latest;
  String? _streamError;
  bool _loadingHistory = true;

  @override
  void initState() {
    super.initState();
    _loadHistory();
    _connect();
  }

  Future<void> _loadHistory() async {
    try {
      final samples = await widget.apiClient.getContainerMetrics(
        widget.serverId,
        widget.containerId,
      );
      if (!mounted) return;
      setState(() {
        _cpuHistory.addAll(samples.map((s) => s.cpuPercent));
        _memHistory.addAll(samples.map((s) => s.memPercent));
        if (samples.isNotEmpty) _latest = samples.last;
      });
    } catch (_) {
      // Live stream still works without history; a chart just starts empty.
    } finally {
      if (mounted) setState(() => _loadingHistory = false);
    }
  }

  void _connect() {
    final uri = widget.apiClient.serverStreamUri(widget.serverId);
    final channel = WebSocketChannel.connect(uri);
    _channel = channel;
    _sub = channel.stream.listen(
      (data) {
        final update = ServerUpdate.fromJson(
          jsonDecode(data as String) as Map<String, dynamic>,
        );
        ContainerResourceUsage? mine;
        for (final u in update.containerStats) {
          if (u.containerId == widget.containerId) {
            mine = u;
            break;
          }
        }
        if (mine == null) return;
        setState(() {
          _cpuHistory.add(mine!.cpuPercent);
          _memHistory.add(mine.memPercent);
          _latest = mine;
        });
      },
      onError: (Object e) {
        setState(() => _streamError = 'Live updates disconnected: $e');
      },
      onDone: () {
        setState(() => _streamError = 'Live updates disconnected');
      },
    );
  }

  @override
  void dispose() {
    _sub?.cancel();
    _channel?.sink.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (widget.containerState != 'running' && _latest == null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            _loadingHistory
                ? 'Loading…'
                : 'This container isn\'t running — start it to see live '
                      'resource usage.',
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.bodyMedium,
          ),
        ),
      );
    }

    final latest = _latest;
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        if (_streamError != null)
          Padding(
            padding: const EdgeInsets.only(bottom: 16),
            child: Text(
              _streamError!,
              style: const TextStyle(color: AppColors.warning),
            ),
          ),
        if (latest == null)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: 16),
            child: Center(child: CircularProgressIndicator()),
          )
        else
          Wrap(
            spacing: 12,
            runSpacing: 12,
            children: [
              _StatTile(label: 'CPU', value: formatCores(latest.cpuPercent)),
              _StatTile(
                label: 'Memory',
                value:
                    '${formatBytes(latest.memUsageBytes)} / '
                    '${latest.memLimitBytes == 0 ? '∞' : formatBytes(latest.memLimitBytes)}',
                sublabel: '${latest.memPercent.toStringAsFixed(1)}%',
              ),
              _StatTile(
                label: 'Network I/O',
                value:
                    '↓ ${formatBytes(latest.netRxBytes)} / '
                    '↑ ${formatBytes(latest.netTxBytes)}',
              ),
              _StatTile(
                label: 'Block I/O',
                value:
                    'R ${formatBytes(latest.blockReadBytes)} / '
                    'W ${formatBytes(latest.blockWriteBytes)}',
              ),
              _StatTile(label: 'Processes', value: '${latest.pids}'),
            ],
          ),
        const SizedBox(height: 24),
        _MetricChart(
          label: 'CPU (cores)',
          values: _cpuHistory,
          color: AppColors.chartLine,
          format: formatCores,
          // Can exceed one core, so scale to the data (min. one core).
          maxY: null,
        ),
        const SizedBox(height: 16),
        _MetricChart(
          label: 'Memory %',
          values: _memHistory,
          color: AppColors.chartLine,
        ),
        const SizedBox(height: 24),
        ContainerInsightsPanel(
          apiClient: widget.apiClient,
          serverId: widget.serverId,
          containerId: widget.containerId,
          detailFuture: widget.detailFuture,
          onReviewLimits: widget.onReviewLimits,
        ),
      ],
    );
  }
}

class _StatTile extends StatelessWidget {
  final String label;
  final String value;
  final String? sublabel;

  const _StatTile({required this.label, required this.value, this.sublabel});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 160,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: Theme.of(context).textTheme.bodySmall),
          const SizedBox(height: 4),
          Text(
            value,
            style: Theme.of(
              context,
            ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold),
          ),
          if (sublabel != null)
            Text(sublabel!, style: Theme.of(context).textTheme.bodySmall),
        ],
      ),
    );
  }
}

/// Same sparkline shape as server_detail_screen.dart's _MetricChart — kept
/// as a separate private copy since a container's chart scale/labels are
/// specific to this screen, not worth sharing a public widget for two call
/// sites.
class _MetricChart extends StatelessWidget {
  final String label;
  final List<double> values;
  final Color color;
  final String Function(double value) format;
  // Fixed y-axis top; null scales to the data (never below 100).
  final double? maxY;

  const _MetricChart({
    required this.label,
    required this.values,
    required this.color,
    this.format = _formatPercent,
    this.maxY = 100,
  });

  static String _formatPercent(double v) => '${v.toStringAsFixed(0)}%';

  @override
  Widget build(BuildContext context) {
    final current = values.isEmpty ? null : values.last;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            Text(label, style: Theme.of(context).textTheme.titleSmall),
            Text(
              current == null ? '—' : format(current),
              style: Theme.of(
                context,
              ).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.bold),
            ),
          ],
        ),
        const SizedBox(height: 8),
        SizedBox(
          height: 80,
          child: values.length < 2
              ? Center(
                  child: Text(
                    'Waiting for data…',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                )
              : LineChart(
                  LineChartData(
                    minY: 0,
                    maxY:
                        maxY ??
                        values.fold<double>(100, (m, v) => v > m ? v : m) * 1.1,
                    gridData: const FlGridData(show: false),
                    titlesData: const FlTitlesData(show: false),
                    borderData: FlBorderData(show: false),
                    lineTouchData: const LineTouchData(enabled: false),
                    lineBarsData: [
                      LineChartBarData(
                        spots: [
                          for (var i = 0; i < values.length; i++)
                            FlSpot(i.toDouble(), values[i]),
                        ],
                        isCurved: true,
                        color: color,
                        barWidth: 2,
                        dotData: const FlDotData(show: false),
                        belowBarData: BarAreaData(
                          show: true,
                          color: color.withValues(alpha: 0.15),
                        ),
                      ),
                    ],
                  ),
                ),
        ),
      ],
    );
  }
}
