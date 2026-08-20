import 'dart:async';
import 'dart:convert';

import 'package:fl_chart/fl_chart.dart';
import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../models/server.dart';
import '../../models/server_metrics.dart';

class ServerDetailScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String serverName;

  const ServerDetailScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.serverName,
  });

  @override
  State<ServerDetailScreen> createState() => _ServerDetailScreenState();
}

class _ServerDetailScreenState extends State<ServerDetailScreen> {
  late Future<ServerDetail> _detailFuture;
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;

  final List<double> _cpuHistory = [];
  final List<double> _memHistory = [];
  final List<double> _diskHistory = [];
  List<ContainerInfo> _containers = [];
  String? _streamError;

  @override
  void initState() {
    super.initState();
    _detailFuture = widget.apiClient.getServerDetail(widget.serverId);
    _detailFuture.then((detail) {
      if (!mounted) return;
      setState(() => _containers = detail.containers);
    });
    _loadHistory();
    _connect();
  }

  Future<void> _loadHistory() async {
    try {
      final samples =
          await widget.apiClient.getServerMetrics(widget.serverId);
      if (!mounted) return;
      setState(() {
        _cpuHistory.addAll(samples.map((s) => s.cpuPercent));
        _memHistory.addAll(samples.map((s) => s.memPercent));
        _diskHistory.addAll(samples.map((s) => s.diskPercent));
      });
    } catch (_) {
      // Live stream still works without history; a sparkline just starts
      // empty instead of pre-populated.
    }
  }

  void _connect() {
    final uri = widget.apiClient.serverStreamUri(widget.serverId);
    final channel = WebSocketChannel.connect(uri);
    _channel = channel;
    _sub = channel.stream.listen(
      (data) {
        final update = ServerUpdate.fromJson(
            jsonDecode(data as String) as Map<String, dynamic>);
        setState(() {
          _cpuHistory.add(update.resources.cpuPercent);
          _memHistory.add(update.resources.memPercent);
          _diskHistory.add(update.resources.diskPercent);
          _containers = update.containers;
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

  Color _containerStateColor(String state) {
    switch (state) {
      case 'running':
        return Colors.green;
      case 'exited':
      case 'dead':
        return Colors.red;
      default:
        return Colors.grey;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(widget.serverName)),
      body: FutureBuilder<ServerDetail>(
        future: _detailFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(
                child: Text('Failed to load server: ${snapshot.error}'));
          }
          final server = snapshot.data!.server;
          return ListView(
            padding: const EdgeInsets.all(16),
            children: [
              _ServerHeader(server: server),
              if (_streamError != null)
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(_streamError!,
                      style: const TextStyle(color: Colors.orange)),
                ),
              const SizedBox(height: 24),
              _MetricChart(
                label: 'CPU',
                values: _cpuHistory,
                color: Colors.blue,
              ),
              const SizedBox(height: 16),
              _MetricChart(
                label: 'Memory',
                values: _memHistory,
                color: Colors.purple,
              ),
              const SizedBox(height: 16),
              _MetricChart(
                label: 'Disk',
                values: _diskHistory,
                color: Colors.teal,
              ),
              const SizedBox(height: 24),
              Text('Containers', style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              if (_containers.isEmpty)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 8),
                  child: Text('No containers on this server.'),
                )
              else
                ..._containers.map((c) => ListTile(
                      dense: true,
                      contentPadding: EdgeInsets.zero,
                      leading: Icon(Icons.circle,
                          size: 10, color: _containerStateColor(c.state)),
                      title: Text(c.name),
                      subtitle: Text(
                          '${c.containerId.substring(0, c.containerId.length < 12 ? c.containerId.length : 12)} • ${c.state}'),
                    )),
            ],
          );
        },
      ),
    );
  }
}

class _ServerHeader extends StatelessWidget {
  final Server server;

  const _ServerHeader({required this.server});

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(
          Icons.dns,
          color: server.status == 'online' ? Colors.green : Colors.grey,
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(server.hostname,
                  style: Theme.of(context).textTheme.bodyMedium),
              Text('${server.os}/${server.arch} • ${server.status}',
                  style: Theme.of(context).textTheme.bodySmall),
            ],
          ),
        ),
      ],
    );
  }
}

class _MetricChart extends StatelessWidget {
  final String label;
  final List<double> values;
  final Color color;

  const _MetricChart({
    required this.label,
    required this.values,
    required this.color,
  });

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
              current == null ? '—' : '${current.toStringAsFixed(0)}%',
              style: Theme.of(context)
                  .textTheme
                  .titleSmall
                  ?.copyWith(fontWeight: FontWeight.bold),
            ),
          ],
        ),
        const SizedBox(height: 8),
        SizedBox(
          height: 80,
          child: values.length < 2
              ? Center(
                  child: Text('Waiting for data…',
                      style: Theme.of(context).textTheme.bodySmall))
              : LineChart(
                  LineChartData(
                    minY: 0,
                    maxY: 100,
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
