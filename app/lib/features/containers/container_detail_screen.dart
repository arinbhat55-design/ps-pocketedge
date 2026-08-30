import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import '../../models/server_metrics.dart';

/// Full detail for one container: the cheap [ContainerInfo] fields (already
/// known from the list/stream, shown immediately) plus the expensive
/// [ContainerDetail] fields (env vars, restart policy, health), fetched on
/// demand from the owning agent via a live ContainerInspect.
class ContainerDetailScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String serverName;
  final ContainerInfo container;

  const ContainerDetailScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.serverName,
    required this.container,
  });

  @override
  State<ContainerDetailScreen> createState() => _ContainerDetailScreenState();
}

class _ContainerDetailScreenState extends State<ContainerDetailScreen> {
  late Future<ContainerDetail> _detailFuture;

  @override
  void initState() {
    super.initState();
    _detailFuture = widget.apiClient
        .inspectContainer(widget.serverId, widget.container.containerId);
  }

  String _uptime(DateTime? createdAt) {
    if (createdAt == null) return '—';
    final d = DateTime.now().difference(createdAt);
    if (d.inDays > 0) return '${d.inDays}d ${d.inHours % 24}h';
    if (d.inHours > 0) return '${d.inHours}h ${d.inMinutes % 60}m';
    return '${d.inMinutes}m';
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.container;
    return Scaffold(
      appBar: AppBar(title: Text(c.name)),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Row(
            children: [
              Icon(Icons.circle, size: 10, color: containerStateColor(c.state)),
              const SizedBox(width: 8),
              Text(c.status?.isNotEmpty == true ? c.status! : c.state),
            ],
          ),
          const SizedBox(height: 16),
          _DetailSection(title: 'Overview', rows: [
            _DetailRow('Container ID', c.containerId),
            _DetailRow('Server', widget.serverName),
            _DetailRow('Image', c.image ?? '—'),
            if (c.imageId != null && c.imageId!.isNotEmpty)
              _DetailRow('Image ID', c.imageId!),
            _DetailRow('Created',
                c.createdAt == null ? '—' : c.createdAt!.toLocal().toString()),
            _DetailRow('Uptime', _uptime(c.createdAt)),
          ]),
          const SizedBox(height: 16),
          _DetailSection(
            title: 'Ports',
            rows: c.ports.isEmpty
                ? [_DetailRow('', 'No published ports')]
                : c.ports
                    .map((p) => _DetailRow(
                        '${p.privatePort}/${p.type}',
                        p.publicPort == 0
                            ? 'not published'
                            : '${p.ip.isEmpty ? '0.0.0.0' : p.ip}:${p.publicPort}'))
                    .toList(),
          ),
          const SizedBox(height: 16),
          _DetailSection(
            title: 'Networks',
            rows: c.networks.isEmpty
                ? [_DetailRow('', 'No networks')]
                : c.networks
                    .map((n) => _DetailRow(n.name, n.ipAddress.isEmpty ? '—' : n.ipAddress))
                    .toList(),
          ),
          const SizedBox(height: 16),
          _DetailSection(
            title: 'Volumes / Mounts',
            rows: c.mounts.isEmpty
                ? [_DetailRow('', 'No mounts')]
                : c.mounts
                    .map((m) => _DetailRow(
                        m.name.isEmpty ? m.source : m.name,
                        '${m.destination} (${m.readWrite ? 'rw' : 'ro'})'))
                    .toList(),
          ),
          const SizedBox(height: 16),
          Text('Details', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          FutureBuilder<ContainerDetail>(
            future: _detailFuture,
            builder: (context, snapshot) {
              if (snapshot.connectionState == ConnectionState.waiting) {
                return const Padding(
                  padding: EdgeInsets.symmetric(vertical: 16),
                  child: Center(child: CircularProgressIndicator()),
                );
              }
              if (snapshot.hasError) {
                return Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Text(
                    'Failed to load container details: ${snapshot.error}',
                    style: const TextStyle(color: Colors.orange),
                  ),
                );
              }
              final detail = snapshot.data!;
              return Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  _DetailSection(title: 'Restart policy', rows: [
                    _DetailRow('Policy',
                        detail.restartPolicyName.isEmpty ? '—' : detail.restartPolicyName),
                    if (detail.restartPolicyMaxRetryCount > 0)
                      _DetailRow('Max retries', '${detail.restartPolicyMaxRetryCount}'),
                    _DetailRow('Restart count', '${detail.restartCount}'),
                  ]),
                  const SizedBox(height: 16),
                  _DetailSection(title: 'Health', rows: [
                    _DetailRow('Status',
                        detail.healthStatus.isEmpty ? 'no healthcheck configured' : detail.healthStatus),
                    if (detail.healthFailingStreak > 0)
                      _DetailRow('Failing streak', '${detail.healthFailingStreak}'),
                  ]),
                  const SizedBox(height: 16),
                  _DetailSection(
                    title: 'Environment variables',
                    rows: detail.env.isEmpty
                        ? [_DetailRow('', 'No environment variables')]
                        : detail.env.map((e) {
                            final parts = e.split('=');
                            final key = parts.first;
                            final value = parts.length > 1 ? parts.sublist(1).join('=') : '';
                            return _DetailRow(key, value);
                          }).toList(),
                  ),
                ],
              );
            },
          ),
        ],
      ),
    );
  }
}

class _DetailRow {
  final String label;
  final String value;
  const _DetailRow(this.label, this.value);
}

class _DetailSection extends StatelessWidget {
  final String title;
  final List<_DetailRow> rows;

  const _DetailSection({required this.title, required this.rows});

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: Theme.of(context).textTheme.titleSmall),
        const SizedBox(height: 4),
        for (final row in rows)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 2),
            child: row.label.isEmpty
                ? Text(row.value, style: Theme.of(context).textTheme.bodySmall)
                : Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      SizedBox(
                        width: 140,
                        child: Text(row.label,
                            style: Theme.of(context).textTheme.bodySmall),
                      ),
                      Expanded(
                        child: Text(row.value,
                            style: Theme.of(context).textTheme.bodyMedium),
                      ),
                    ],
                  ),
          ),
      ],
    );
  }
}
