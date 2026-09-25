import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../../models/network.dart';
import 'network_connect_dialog.dart';
import 'network_create_dialog.dart';
import '../../theme/app_theme.dart';

/// Network management: fleet-wide (or per-server) network inventory with
/// create/remove and connecting/disconnecting containers.
class NetworkListScreen extends StatefulWidget {
  final ApiClient apiClient;

  const NetworkListScreen({super.key, required this.apiClient});

  @override
  State<NetworkListScreen> createState() => _NetworkListScreenState();
}

class _NetworkListScreenState extends State<NetworkListScreen> {
  late Future<List<NetworkSummary>> _networksFuture;
  String? _selectedServerId;
  Map<String, String> _serverNames = {};

  @override
  void initState() {
    super.initState();
    _networksFuture = _load();
  }

  Future<List<NetworkSummary>> _load() {
    return widget.apiClient.listNetworks(serverId: _selectedServerId);
  }

  void _refresh() {
    setState(() {
      _networksFuture = _load();
    });
  }

  Future<void> _openCreate() async {
    final serverName = _selectedServerId == null
        ? null
        : _serverNames[_selectedServerId];
    final created = await showCreateNetworkDialog(
      context,
      apiClient: widget.apiClient,
      serverId: _selectedServerId,
      serverName: serverName,
    );
    if (created == true) _refresh();
  }

  Future<void> _openConnect(NetworkSummary network) async {
    final connected = await showNetworkConnectDialog(
      context,
      apiClient: widget.apiClient,
      serverId: network.serverId,
      networkId: network.id,
      networkName: network.name,
    );
    if (connected == true) _refresh();
  }

  Future<void> _disconnect(NetworkSummary network, String containerId) async {
    try {
      final result = await widget.apiClient.disconnectContainerFromNetwork(
        network.serverId,
        network.id,
        containerId,
      );
      if (!mounted) return;
      if (!result.success) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(result.error ?? 'Disconnect failed')),
        );
        return;
      }
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Disconnect failed: $e')));
      }
    }
  }

  Future<void> _removeNetwork(NetworkSummary network) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Remove network?'),
        content: Text(
          'This permanently deletes "${network.name}" from ${network.serverName}.'
          '${network.containerIds.isNotEmpty ? '\n\nIt currently has ${network.containerIds.length} container(s) attached — removal will fail until they are disconnected.' : ''}',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: AppColors.failed),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    try {
      final result = await widget.apiClient.removeNetwork(
        network.serverId,
        network.id,
      );
      if (!mounted) return;
      if (!result.success) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(result.error ?? 'Remove failed')),
        );
        return;
      }
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to remove network: $e')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final create = PrimaryAction(
      label: 'Create network',
      icon: Icons.add,
      onPressed: _openCreate,
    );
    return Scaffold(
      appBar: AppBar(
        title: const Text('Networks'),
        actions: [?create.appBarAction(context)],
      ),
      floatingActionButton: create.fab(context),
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const PageIntro(
            description:
                'Docker networks on each server. Containers on the same '
                'network can reach each other by name.',
          ),
          Expanded(
            child: FutureBuilder<List<NetworkSummary>>(
              future: _networksFuture,
              builder: (context, snapshot) {
                if (snapshot.connectionState == ConnectionState.waiting) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError) {
                  return StateMessage.error(
                    what: 'networks',
                    error: snapshot.error,
                    onRetry: _refresh,
                  );
                }
                final all = snapshot.data ?? [];
                // Merged rather than replaced: the listing is filtered by server,
                // so rebuilding from it alone would hide the other servers' chips.
                _serverNames = {
                  ..._serverNames,
                  for (final n in all) n.serverId: n.serverName,
                };
                final servers = _serverNames;

                return Column(
                  children: [
                    Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: Space.lg,
                        vertical: Space.md,
                      ),
                      child: Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        children: [
                          ChoiceChip(
                            label: const Text('All servers'),
                            selected: _selectedServerId == null,
                            onSelected: (_) {
                              setState(() => _selectedServerId = null);
                              _refresh();
                            },
                          ),
                          for (final entry in servers.entries)
                            ChoiceChip(
                              label: Text(entry.value),
                              selected: _selectedServerId == entry.key,
                              onSelected: (_) {
                                setState(() => _selectedServerId = entry.key);
                                _refresh();
                              },
                            ),
                        ],
                      ),
                    ),
                    Expanded(
                      child: all.isEmpty
                          ? StateMessage(
                              icon: Icons.hub_outlined,
                              title: _selectedServerId == null
                                  ? 'No networks yet'
                                  : 'No networks on this server',
                              message:
                                  'Custom networks let containers reach each other '
                                  'by name. Docker\'s built-in networks appear once '
                                  'a server has reported in.',
                              actionLabel: 'Create network',
                              actionIcon: Icons.add,
                              onAction: _openCreate,
                            )
                          : _NetworkTable(
                              networks: all,
                              onConnect: _openConnect,
                              onDisconnect: _disconnect,
                              onRemove: _removeNetwork,
                            ),
                    ),
                  ],
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

/// The fleet-network table: an [Axis.horizontal]-scrolling [DataTable] with
/// its own [ScrollController]s (needed for [Scrollbar.thumbVisibility] to
/// actually attach — without one the thumb paints but dragging it does
/// nothing) and mouse opted into drag-to-scroll, since the platform default
/// only allows touch/stylus/trackpad there. Attached containers are shown
/// as a nested expandable row rather than a column, since their count is
/// unbounded.
class _NetworkTable extends StatefulWidget {
  final List<NetworkSummary> networks;
  final void Function(NetworkSummary) onConnect;
  final void Function(NetworkSummary, String) onDisconnect;
  final void Function(NetworkSummary) onRemove;

  const _NetworkTable({
    required this.networks,
    required this.onConnect,
    required this.onDisconnect,
    required this.onRemove,
  });

  @override
  State<_NetworkTable> createState() => _NetworkTableState();
}

class _NetworkTableState extends State<_NetworkTable> {
  final _verticalController = ScrollController();
  final _horizontalController = ScrollController();
  final Set<String> _expanded = {};

  @override
  void dispose() {
    _verticalController.dispose();
    _horizontalController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ScrollConfiguration(
      behavior: ScrollConfiguration.of(context).copyWith(
        dragDevices: {
          PointerDeviceKind.touch,
          PointerDeviceKind.mouse,
          PointerDeviceKind.trackpad,
          PointerDeviceKind.stylus,
        },
      ),
      child: Scrollbar(
        controller: _horizontalController,
        thumbVisibility: true,
        trackVisibility: true,
        notificationPredicate: (notification) =>
            notification.metrics.axis == Axis.horizontal,
        child: SingleChildScrollView(
          controller: _horizontalController,
          scrollDirection: Axis.horizontal,
          child: Scrollbar(
            controller: _verticalController,
            thumbVisibility: true,
            notificationPredicate: (notification) =>
                notification.metrics.axis == Axis.vertical,
            child: SingleChildScrollView(
              controller: _verticalController,
              padding: const EdgeInsets.only(bottom: 12, right: 12),
              child: DataTable(
                columns: const [
                  DataColumn(label: Text('')),
                  DataColumn(label: Text('Name')),
                  DataColumn(label: Text('Server')),
                  DataColumn(label: Text('Driver')),
                  DataColumn(label: Text('Scope')),
                  DataColumn(label: Text('Internal')),
                  DataColumn(label: Text('Containers')),
                  DataColumn(label: Text('Actions')),
                ],
                rows: widget.networks.expand(_buildRows).toList(),
              ),
            ),
          ),
        ),
      ),
    );
  }

  List<DataRow> _buildRows(NetworkSummary network) {
    final expanded = _expanded.contains(network.id);
    final rows = [
      DataRow(
        cells: [
          DataCell(
            network.containerIds.isEmpty
                ? const SizedBox(width: 18)
                : IconButton(
                    icon: Icon(
                      expanded ? Icons.expand_more : Icons.chevron_right,
                      size: 18,
                    ),
                    visualDensity: VisualDensity.compact,
                    onPressed: () => setState(() {
                      if (expanded) {
                        _expanded.remove(network.id);
                      } else {
                        _expanded.add(network.id);
                      }
                    }),
                  ),
          ),
          DataCell(Text(network.name)),
          DataCell(Text(network.serverName)),
          DataCell(Text(network.driver)),
          DataCell(Text(network.scope)),
          DataCell(Text(network.internal ? 'Yes' : 'No')),
          DataCell(Text('${network.containerIds.length}')),
          DataCell(
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                IconButton(
                  icon: const Icon(Icons.link, size: 18),
                  tooltip: 'Connect container',
                  visualDensity: VisualDensity.compact,
                  onPressed: () => widget.onConnect(network),
                ),
                IconButton(
                  icon: const Icon(Icons.delete_outline, size: 18),
                  tooltip: 'Remove network',
                  visualDensity: VisualDensity.compact,
                  color: AppColors.failed,
                  onPressed: () => widget.onRemove(network),
                ),
              ],
            ),
          ),
        ],
      ),
    ];

    if (expanded) {
      for (final containerId in network.containerIds) {
        final shortId = containerId.length > 12
            ? containerId.substring(0, 12)
            : containerId;
        rows.add(
          DataRow(
            color: WidgetStateProperty.all(
              Theme.of(context).colorScheme.surfaceContainerHighest,
            ),
            cells: [
              const DataCell(SizedBox(width: 18)),
              DataCell(
                Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Icon(Icons.view_in_ar_outlined, size: 16),
                    const SizedBox(width: 6),
                    Text(
                      shortId,
                      style: const TextStyle(fontFamily: 'monospace'),
                    ),
                  ],
                ),
              ),
              const DataCell(SizedBox()),
              const DataCell(SizedBox()),
              const DataCell(SizedBox()),
              const DataCell(SizedBox()),
              const DataCell(SizedBox()),
              DataCell(
                IconButton(
                  icon: const Icon(Icons.link_off, size: 18),
                  tooltip: 'Disconnect',
                  visualDensity: VisualDensity.compact,
                  onPressed: () => widget.onDisconnect(network, containerId),
                ),
              ),
            ],
          ),
        );
      }
    }

    return rows;
  }
}
