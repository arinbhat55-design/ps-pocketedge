import 'dart:async';

import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/server.dart';
import '../../theme/app_theme.dart';
import '../../widgets/formatting.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';
import '../deployments/catalog_screen.dart';
import '../deployments/deployment_status_screen.dart';
import 'add_server_dialog.dart';
import 'remove_server.dart';
import 'server_detail_screen.dart';

class ServerListScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const ServerListScreen({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
  });

  @override
  State<ServerListScreen> createState() => _ServerListScreenState();
}

class _ServerListScreenState extends State<ServerListScreen> {
  late Future<List<Server>> _serversFuture;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _serversFuture = widget.apiClient.listServers();
    _poll = Timer.periodic(kListPollInterval, (_) {
      if (mounted && TickerMode.valuesOf(context).enabled) _refresh();
    });
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _refresh() async {
    setState(() {
      _serversFuture = widget.apiClient.listServers();
    });
    await _serversFuture;
  }

  Future<void> _openDeploy(Server server) async {
    final deploymentId = await Navigator.of(context).push<String>(
      MaterialPageRoute(
        builder: (_) => CatalogScreen(
          apiClient: widget.apiClient,
          serverId: server.id,
          serverName: server.name,
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

  Future<void> _openDetail(Server server) async {
    final removed = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => ServerDetailScreen(
          apiClient: widget.apiClient,
          serverId: server.id,
          serverName: server.name,
          isAdmin: widget.isAdmin,
        ),
      ),
    );
    if (removed == true && mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('Removed ${server.name}')));
      await _refresh();
    }
  }

  Future<void> _removeServer(Server server) async {
    final removed = await confirmAndRemoveServer(
      context,
      apiClient: widget.apiClient,
      serverId: server.id,
      serverName: server.name,
    );
    if (!removed || !mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text('Removed ${server.name}')));
    await _refresh();
  }

  Future<void> _openAddServer() async {
    await showDialog<void>(
      context: context,
      builder: (_) => AddServerDialog(apiClient: widget.apiClient),
    );
    // A freshly enrolled server won't show up until it's actually running
    // and has enrolled, so this refresh is best-effort, not guaranteed to
    // show the new server immediately.
    await _refresh();
  }

  @override
  Widget build(BuildContext context) {
    final addServer = PrimaryAction(
      label: 'Add server',
      icon: Icons.add,
      onPressed: _openAddServer,
    );
    return Scaffold(
      appBar: AppBar(
        title: const Text('Servers'),
        actions: [if (widget.isAdmin) ?addServer.appBarAction(context)],
      ),
      floatingActionButton: widget.isAdmin ? addServer.fab(context) : null,
      body: FutureBuilder<List<Server>>(
        future: _serversFuture,
        builder: (context, snapshot) {
          final servers = snapshot.data;
          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              PageIntro(
                description:
                    'Machines running the agent. Open one for live resources '
                    'and containers, or deploy an app to it.',
                summary: servers == null || servers.isEmpty
                    ? const []
                    : _summary(servers),
              ),
              const SizedBox(height: Space.md),
              Expanded(
                child: RefreshIndicator(
                  onRefresh: _refresh,
                  child: _buildContent(snapshot),
                ),
              ),
            ],
          );
        },
      ),
    );
  }

  List<Widget> _summary(List<Server> servers) {
    final now = DateTime.now();
    final online = servers.where((s) => isServerOnline(s, now: now)).length;
    final down = servers
        .where((s) => serverStatus(s, now: now).tone == StatusTone.failed)
        .length;
    return [
      SummaryStat(
        value: '$online',
        label: 'online',
        color: online > 0 ? AppColors.healthy : null,
      ),
      SummaryStat(
        value: '$down',
        label: 'disconnected',
        color: down > 0 ? AppColors.failed : null,
      ),
      SummaryStat(value: '${servers.length}', label: 'total'),
    ];
  }

  Widget _buildContent(AsyncSnapshot<List<Server>> snapshot) {
    // Background refreshes keep the current cards on screen.
    if (snapshot.connectionState == ConnectionState.waiting &&
        !snapshot.hasData) {
      return const Center(child: CircularProgressIndicator());
    }
    if (snapshot.hasError) {
      return StateMessage.error(
        what: 'servers',
        error: snapshot.error,
        onRetry: _refresh,
      );
    }
    final servers = snapshot.data ?? [];
    if (servers.isEmpty) {
      return StateMessage(
        icon: Icons.dns_outlined,
        title: 'No servers yet',
        message:
            'Add a server to install the agent on it. It shows up here as '
            'soon as the agent connects, ready to deploy to.',
        actionLabel: 'Add server',
        actionIcon: Icons.add,
        onAction: _openAddServer,
      );
    }
    return LayoutBuilder(
      builder: (context, constraints) {
        final available = constraints.maxWidth - Space.lg * 2;
        final columns = available >= 1080
            ? 3
            : available >= 680
            ? 2
            : 1;
        final cardWidth = (available - Space.md * (columns - 1)) / columns;
        return ListView(
          // Room for the FAB under the last card on phones.
          padding: const EdgeInsets.fromLTRB(Space.lg, 0, Space.lg, 88),
          children: [
            Wrap(
              spacing: Space.md,
              runSpacing: Space.md,
              children: [
                for (final server in servers)
                  SizedBox(
                    width: cardWidth,
                    child: _ServerCard(
                      server: server,
                      onOpen: () => _openDetail(server),
                      onDeploy: () => _openDeploy(server),
                      // Admins get a ⋮ menu to remove a server that has
                      // stopped reporting.
                      onRemove: widget.isAdmin
                          ? () => _removeServer(server)
                          : null,
                    ),
                  ),
              ],
            ),
          ],
        );
      },
    );
  }
}

/// One server's summary: name and status first, then its current CPU,
/// memory and disk, then platform detail and Deploy.
class _ServerCard extends StatelessWidget {
  final Server server;
  final VoidCallback onOpen;
  final VoidCallback onDeploy;
  final VoidCallback? onRemove;

  const _ServerCard({
    required this.server,
    required this.onOpen,
    required this.onDeploy,
    this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final status = serverStatus(server);
    final stale = status.tone != StatusTone.healthy;
    final resources = server.lastResources;
    final heartbeat = server.lastHeartbeatAt;

    return Card(
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onOpen,
        child: Padding(
          padding: const EdgeInsets.all(Space.lg),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      server.name,
                      style: theme.textTheme.titleMedium,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  const SizedBox(width: Space.sm),
                  StatusPill.of(status),
                  if (onRemove != null) ...[
                    const SizedBox(width: Space.xs),
                    SizedBox(
                      width: 28,
                      height: 28,
                      child: ServerMenuButton(
                        dense: true,
                        canRemove: status.tone == StatusTone.failed,
                        onRemove: onRemove!,
                      ),
                    ),
                  ],
                ],
              ),
              const SizedBox(height: Space.xs),
              Text(
                [
                  if (server.hostname.isNotEmpty) server.hostname,
                  '${server.os}/${server.arch}',
                ].join('  ·  '),
                style: AppText.mono(context),
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: Space.lg),
              if (resources == null)
                Text('No resource data yet', style: theme.textTheme.bodySmall)
              else
                Row(
                  children: [
                    Expanded(
                      child: _UsageMeter(
                        label: 'CPU',
                        percent: resources.cpuPercent,
                        stale: stale,
                      ),
                    ),
                    const SizedBox(width: Space.md),
                    Expanded(
                      child: _UsageMeter(
                        label: 'Memory',
                        percent: resources.memPercent,
                        stale: stale,
                      ),
                    ),
                    const SizedBox(width: Space.md),
                    Expanded(
                      child: _UsageMeter(
                        label: 'Disk',
                        percent: resources.diskPercent,
                        stale: stale,
                      ),
                    ),
                  ],
                ),
              const SizedBox(height: Space.md),
              Row(
                children: [
                  Expanded(
                    child: Text(
                      heartbeat == null
                          ? 'Never reported'
                          : 'Seen ${formatAgo(heartbeat)}',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: AppColors.textMuted,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  FilledButton.tonalIcon(
                    onPressed: onDeploy,
                    icon: const Icon(Icons.rocket_launch, size: 16),
                    label: const Text('Deploy'),
                    style: FilledButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// Label, value, and a thin bar. The value keeps the normal text color
/// unless usage is high; the bar is quiet until then too. Dimmed when the
/// server is disconnected, since the number is no longer current.
class _UsageMeter extends StatelessWidget {
  final String label;
  final double percent;
  final bool stale;

  const _UsageMeter({
    required this.label,
    required this.percent,
    required this.stale,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final tone = usageTone(percent);
    final Color valueColor;
    final Color barColor;
    if (stale) {
      valueColor = AppColors.textMuted;
      barColor = AppColors.neutral.withValues(alpha: 0.5);
    } else if (tone == StatusTone.healthy) {
      valueColor = theme.colorScheme.onSurface;
      barColor = AppColors.chartLine;
    } else {
      valueColor = tone.color;
      barColor = tone.color;
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(label, style: theme.textTheme.bodySmall),
        Text(
          '${percent.toStringAsFixed(0)}%',
          style: theme.textTheme.titleMedium?.copyWith(
            color: valueColor,
            fontFeatures: const [FontFeature.tabularFigures()],
          ),
        ),
        const SizedBox(height: Space.xs),
        ClipRRect(
          borderRadius: BorderRadius.circular(2),
          child: LinearProgressIndicator(
            value: (percent / 100).clamp(0, 1).toDouble(),
            minHeight: 4,
            color: barColor,
            backgroundColor: theme.colorScheme.surfaceContainerHighest,
          ),
        ),
      ],
    );
  }
}
