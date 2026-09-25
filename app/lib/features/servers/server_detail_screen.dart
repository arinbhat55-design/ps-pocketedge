import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../models/server.dart';
import '../../models/server_metrics.dart';
import '../../theme/app_theme.dart';
import '../../widgets/formatting.dart';
import '../../widgets/metric_card.dart';
import '../../widgets/notice_banner.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';
import '../containers/container_detail_screen.dart';
import '../deployments/catalog_screen.dart';
import '../deployments/deployment_status_screen.dart';
import 'remove_server.dart';

/// Time ranges offered for the resource charts; the control plane's
/// metrics endpoint takes any Go duration as `since`.
const _ranges = [
  Duration(minutes: 15),
  Duration(hours: 1),
  Duration(hours: 6),
  Duration(hours: 24),
];

class ServerDetailScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String serverName;
  // Admins can remove a server that's stopped reporting.
  final bool isAdmin;

  const ServerDetailScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.serverName,
    this.isAdmin = false,
  });

  @override
  State<ServerDetailScreen> createState() => _ServerDetailScreenState();
}

class _ServerDetailScreenState extends State<ServerDetailScreen> {
  late Future<ServerDetail> _detailFuture;
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;
  // Re-renders periodically so "12 s ago" / stale detection stay current
  // even when no updates arrive — which is exactly the disconnected case.
  Timer? _clock;

  Duration _range = const Duration(hours: 1);
  bool _loadingHistory = false;
  List<MetricSample> _samples = [];
  List<ContainerInfo> _containers = [];
  DateTime? _lastUpdateAt;
  String? _streamError;
  Server? _server;
  bool _removing = false;

  @override
  void initState() {
    super.initState();
    _detailFuture = _loadDetail();
    _loadHistory();
    _connect();
    _clock = Timer.periodic(const Duration(seconds: 15), (_) {
      if (mounted) setState(() {});
    });
  }

  Future<ServerDetail> _loadDetail() {
    final future = widget.apiClient.getServerDetail(widget.serverId);
    future.then((detail) {
      if (!mounted) return;
      setState(() {
        _containers = detail.containers;
        _server = detail.server;
        _lastUpdateAt ??= detail.server.lastHeartbeatAt;
      });
    }, onError: (_) {});
    return future;
  }

  Future<void> _loadHistory() async {
    setState(() => _loadingHistory = true);
    try {
      final samples = await widget.apiClient.getServerMetrics(
        widget.serverId,
        since: goDuration(_range),
      );
      if (!mounted) return;
      setState(() {
        // Keep any live samples newer than the fetched history.
        final newest = samples.isEmpty ? null : samples.last.recordedAt;
        _samples = [
          ...samples,
          for (final s in _samples)
            if (newest == null || s.recordedAt.isAfter(newest)) s,
        ];
      });
    } catch (_) {
      // Live stream still works without history; the charts just start
      // from the first live sample.
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
        setState(() {
          _samples.add(
            MetricSample(
              recordedAt: update.updatedAt,
              cpuPercent: update.resources.cpuPercent,
              memPercent: update.resources.memPercent,
              diskPercent: update.resources.diskPercent,
            ),
          );
          _containers = update.containers;
          _lastUpdateAt = update.updatedAt;
          _streamError = null;
        });
      },
      onError: (Object e) {
        if (mounted) setState(() => _streamError = '$e');
      },
      onDone: () {
        if (mounted) setState(() => _streamError ??= 'connection closed');
      },
    );
  }

  void _reconnect() {
    _sub?.cancel();
    _channel?.sink.close();
    setState(() => _streamError = null);
    _connect();
    _loadHistory();
  }

  void _retryDetail() {
    setState(() {
      _detailFuture = _loadDetail();
    });
  }

  Future<void> _openDeploy() async {
    final deploymentId = await Navigator.of(context).push<String>(
      MaterialPageRoute(
        builder: (_) => CatalogScreen(
          apiClient: widget.apiClient,
          serverId: widget.serverId,
          serverName: widget.serverName,
        ),
      ),
    );
    if (deploymentId == null || !mounted) return;
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => DeploymentStatusScreen(
          apiClient: widget.apiClient,
          deploymentId: deploymentId,
        ),
      ),
    );
  }

  /// Only a server whose agent has stopped reporting can be removed; the
  /// control plane also refuses while the agent is still connected.
  bool get _canRemove {
    final server = _server;
    if (server == null) return false;
    final status = serverStatusFrom(
      status: server.status,
      lastHeartbeatAt: _lastUpdateAt ?? server.lastHeartbeatAt,
    );
    return status.tone == StatusTone.failed;
  }

  Widget _buildMenu(BuildContext context) {
    return ServerMenuButton(
      canRemove: _canRemove && !_removing,
      onRemove: _remove,
    );
  }

  Future<void> _remove() async {
    setState(() => _removing = true);
    final removed = await confirmAndRemoveServer(
      context,
      apiClient: widget.apiClient,
      serverId: widget.serverId,
      serverName: widget.serverName,
    );
    if (!mounted) return;
    if (removed) {
      Navigator.of(context).pop(true);
    } else {
      setState(() => _removing = false);
    }
  }

  @override
  void dispose() {
    _clock?.cancel();
    _sub?.cancel();
    _channel?.sink.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final deploy = PrimaryAction(
      label: 'Deploy app',
      icon: Icons.rocket_launch,
      onPressed: _openDeploy,
    );
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.serverName),
        actions: [
          ?deploy.appBarAction(context),
          if (widget.isAdmin) _buildMenu(context),
        ],
      ),
      floatingActionButton: deploy.fab(context),
      body: FutureBuilder<ServerDetail>(
        future: _detailFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return StateMessage.error(
              what: 'this server',
              error: snapshot.error,
              onRetry: _retryDetail,
            );
          }
          return _buildBody(context, snapshot.data!.server);
        },
      ),
    );
  }

  Widget _buildBody(BuildContext context, Server server) {
    final now = DateTime.now();
    final lastSeen = _lastUpdateAt ?? server.lastHeartbeatAt;
    final status = serverStatusFrom(
      status: server.status,
      lastHeartbeatAt: lastSeen,
      now: now,
    );
    final agentDown = status.tone == StatusTone.failed;
    final stale = agentDown || _streamError != null;

    return ListView(
      padding: const EdgeInsets.fromLTRB(Space.lg, Space.lg, Space.lg, 88),
      children: [
        _ServerHeader(
          server: server,
          status: status,
          lastSeen: lastSeen,
          now: now,
        ),
        if (agentDown) ...[
          const SizedBox(height: Space.md),
          NoticeBanner(
            tone: StatusTone.failed,
            icon: Icons.link_off,
            title: 'Server disconnected',
            message: lastSeen == null
                ? 'The agent on this server hasn\'t reported yet.'
                : 'The agent last reported ${formatAgo(lastSeen, now: now)}. '
                      'Values below are the last ones received.',
          ),
        ] else if (_streamError != null) ...[
          const SizedBox(height: Space.md),
          NoticeBanner(
            tone: StatusTone.warning,
            icon: Icons.sync_problem,
            title: 'Live updates paused',
            message:
                'Lost the live connection to the control plane. Values '
                'below may be out of date.',
            actionLabel: 'Reconnect',
            onAction: _reconnect,
          ),
        ],
        const SizedBox(height: Space.xl),
        _SectionHeader(
          title: 'Resources',
          trailing: SegmentedButton<Duration>(
            showSelectedIcon: false,
            segments: [
              for (final r in _ranges)
                ButtonSegment(value: r, label: Text(formatRange(r))),
            ],
            selected: {_range},
            onSelectionChanged: (s) {
              setState(() => _range = s.first);
              _loadHistory();
            },
          ),
        ),
        SizedBox(
          height: 2,
          child: _loadingHistory ? const LinearProgressIndicator() : null,
        ),
        const SizedBox(height: Space.sm),
        _MetricGrid(
          children: [
            for (final (label, icon, pick)
                in <(String, IconData, double Function(MetricSample))>[
                  ('CPU', Icons.memory, (s) => s.cpuPercent),
                  ('Memory', Icons.developer_board, (s) => s.memPercent),
                  ('Disk', Icons.storage, (s) => s.diskPercent),
                ])
              MetricCard(
                label: label,
                icon: icon,
                points: [
                  for (final s in _samples) MetricPoint(s.recordedAt, pick(s)),
                ],
                range: _range,
                now: now,
                stale: stale,
              ),
          ],
        ),
        const SizedBox(height: Space.xl),
        _SectionHeader(
          title: 'Containers',
          trailing: Text(
            '${_containers.length}',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
        const SizedBox(height: Space.sm),
        _buildContainers(context, stale: stale, lastSeen: lastSeen),
      ],
    );
  }

  Widget _buildContainers(
    BuildContext context, {
    required bool stale,
    required DateTime? lastSeen,
  }) {
    final theme = Theme.of(context);
    if (_containers.isEmpty) {
      return Card(
        child: Padding(
          padding: const EdgeInsets.all(Space.lg),
          child: Text(
            'No containers on this server yet. Use Deploy app to start one.',
            style: theme.textTheme.bodyMedium?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ),
      );
    }
    return Card(
      clipBehavior: Clip.antiAlias,
      child: Column(
        children: [
          for (var i = 0; i < _containers.length; i++) ...[
            if (i > 0) const Divider(),
            _ContainerRow(
              container: _containers[i],
              // A stale snapshot isn't live state: say so instead of
              // showing a confident "Running".
              status: stale
                  ? (label: 'Unknown', tone: StatusTone.neutral)
                  : containerStatusDetailed(
                      _containers[i].state,
                      _containers[i].status,
                    ),
              detail: stale
                  ? 'Was ${containerStatus(_containers[i].state).label.toLowerCase()}'
                        '${lastSeen == null ? '' : ' · ${formatAgo(lastSeen)}'}'
                  : _containers[i].status,
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => ContainerDetailScreen(
                    apiClient: widget.apiClient,
                    serverId: widget.serverId,
                    serverName: widget.serverName,
                    container: _containers[i],
                  ),
                ),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _ServerHeader extends StatelessWidget {
  final Server server;
  final StatusLabel status;
  final DateTime? lastSeen;
  final DateTime now;

  const _ServerHeader({
    required this.server,
    required this.status,
    required this.lastSeen,
    required this.now,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(Space.lg),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    server.hostname.isEmpty ? server.name : server.hostname,
                    style: theme.textTheme.titleMedium,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: Space.sm),
                StatusPill.of(status),
              ],
            ),
            const SizedBox(height: Space.xs),
            Text(
              lastSeen == null
                  ? 'No heartbeat received yet'
                  : 'Last heartbeat ${formatAgo(lastSeen!, now: now)}',
              style: theme.textTheme.bodySmall,
            ),
            const SizedBox(height: Space.md),
            Wrap(
              spacing: Space.lg,
              runSpacing: Space.xs,
              children: [
                _Fact('Platform', '${server.os}/${server.arch}'),
                _Fact('Agent', server.agentVersion),
                _Fact('ID', server.id),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// "Label value" with the value in monospace — for the technical facts
/// (platform, versions, IDs) that should be findable but not dominant.
class _Fact extends StatelessWidget {
  final String label;
  final String value;

  const _Fact(this.label, this.value);

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          '$label ',
          style: Theme.of(
            context,
          ).textTheme.bodySmall?.copyWith(color: AppColors.textMuted),
        ),
        SelectableText(value, style: AppText.mono(context)),
      ],
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;
  final Widget? trailing;

  const _SectionHeader({required this.title, this.trailing});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: Space.sm),
      child: Wrap(
        alignment: WrapAlignment.spaceBetween,
        crossAxisAlignment: WrapCrossAlignment.center,
        spacing: Space.md,
        runSpacing: Space.sm,
        children: [
          Text(title, style: Theme.of(context).textTheme.titleMedium),
          ?trailing,
        ],
      ),
    );
  }
}

/// Three metric cards side by side when there's room, stacked on phones.
class _MetricGrid extends StatelessWidget {
  final List<Widget> children;

  const _MetricGrid({required this.children});

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        if (constraints.maxWidth < kTableMinWidth) {
          return Column(
            children: [
              for (var i = 0; i < children.length; i++) ...[
                if (i > 0) const SizedBox(height: Space.md),
                children[i],
              ],
            ],
          );
        }
        return Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            for (var i = 0; i < children.length; i++) ...[
              if (i > 0) const SizedBox(width: Space.md),
              Expanded(child: children[i]),
            ],
          ],
        );
      },
    );
  }
}

class _ContainerRow extends StatelessWidget {
  final ContainerInfo container;
  final StatusLabel status;
  final String? detail;
  final VoidCallback onTap;

  const _ContainerRow({
    required this.container,
    required this.status,
    required this.detail,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final id = container.containerId;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(
          horizontal: Space.lg,
          vertical: Space.md,
        ),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    container.name,
                    style: theme.textTheme.titleSmall,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(
                    [
                      id.length > 12 ? id.substring(0, 12) : id,
                      if (container.image != null) container.image!,
                    ].join('  ·  '),
                    style: AppText.mono(context),
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            const SizedBox(width: Space.md),
            Column(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                StatusPill.of(status),
                if (detail != null && detail!.isNotEmpty) ...[
                  const SizedBox(height: 2),
                  Text(detail!, style: theme.textTheme.bodySmall),
                ],
              ],
            ),
          ],
        ),
      ),
    );
  }
}
