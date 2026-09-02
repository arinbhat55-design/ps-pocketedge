import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart' show formatBytes;
import '../../models/volume.dart';
import 'volume_create_dialog.dart';
import 'volume_delete_dialog.dart';
import 'volume_detail_screen.dart';

/// Volume management: fleet-wide (or per-server) volume inventory with
/// search/filter, create/delete, orphan detection, and usage/metadata
/// browsing.
class VolumeListScreen extends StatefulWidget {
  final ApiClient apiClient;

  const VolumeListScreen({super.key, required this.apiClient});

  @override
  State<VolumeListScreen> createState() => _VolumeListScreenState();
}

class _VolumeListScreenState extends State<VolumeListScreen> {
  late Future<List<VolumeSummary>> _volumesFuture;
  final _searchController = TextEditingController();

  String? _selectedServerId;
  bool _orphanedOnly = false;
  Map<String, String> _serverNames = {};

  @override
  void initState() {
    super.initState();
    _volumesFuture = _load();
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<List<VolumeSummary>> _load() {
    return widget.apiClient.listVolumes(serverId: _selectedServerId);
  }

  void _refresh() {
    setState(() => _volumesFuture = _load());
  }

  List<VolumeSummary> _filter(List<VolumeSummary> volumes) {
    final query = _searchController.text.trim().toLowerCase();
    return volumes.where((v) {
      if (_orphanedOnly && !v.orphaned) return false;
      if (query.isEmpty) return true;
      return v.name.toLowerCase().contains(query);
    }).toList();
  }

  Future<void> _openCreate() async {
    final serverName = _selectedServerId == null
        ? null
        : _serverNames[_selectedServerId];
    final created = await showCreateVolumeDialog(
      context,
      apiClient: widget.apiClient,
      serverId: _selectedServerId,
      serverName: serverName,
    );
    if (created == true) _refresh();
  }

  Future<void> _openDetail(VolumeSummary volume) async {
    final changed = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) =>
            VolumeDetailScreen(apiClient: widget.apiClient, volume: volume),
      ),
    );
    if (changed == true) _refresh();
  }

  Future<void> _delete(VolumeSummary volume) async {
    final deleted = await showVolumeDeleteDialog(
      context,
      apiClient: widget.apiClient,
      volume: volume,
    );
    if (deleted == true) _refresh();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _openCreate,
        icon: const Icon(Icons.add),
        label: const Text('Create volume'),
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 0),
            child: TextField(
              controller: _searchController,
              onChanged: (_) => setState(() {}),
              decoration: const InputDecoration(
                hintText: 'Search by name',
                prefixIcon: Icon(Icons.search),
                isDense: true,
                border: OutlineInputBorder(),
              ),
            ),
          ),
          Expanded(
            child: FutureBuilder<List<VolumeSummary>>(
              future: _volumesFuture,
              builder: (context, snapshot) {
                if (snapshot.connectionState == ConnectionState.waiting) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError) {
                  return Center(
                    child: Text('Failed to load volumes: ${snapshot.error}'),
                  );
                }
                final all = snapshot.data ?? [];
                final servers = <String, String>{
                  for (final v in all) v.serverId: v.serverName,
                };
                _serverNames = servers;
                final volumes = _filter(all);

                return Column(
                  children: [
                    Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 12,
                        vertical: 8,
                      ),
                      child: Wrap(
                        spacing: 8,
                        runSpacing: 8,
                        crossAxisAlignment: WrapCrossAlignment.center,
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
                          const SizedBox(width: 4),
                          const VerticalDivider(width: 1),
                          const SizedBox(width: 4),
                          FilterChip(
                            label: const Text('Orphaned only'),
                            selected: _orphanedOnly,
                            onSelected: (v) =>
                                setState(() => _orphanedOnly = v),
                          ),
                        ],
                      ),
                    ),
                    Expanded(
                      child: volumes.isEmpty
                          ? const Center(child: Text('No volumes found.'))
                          : _VolumeTable(
                              volumes: volumes,
                              onOpenDetail: _openDetail,
                              onDelete: _delete,
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

/// The fleet-volume table: an [Axis.horizontal]-scrolling [DataTable] with
/// its own [ScrollController]s (needed for [Scrollbar.thumbVisibility] to
/// actually attach — without one the thumb paints but dragging it does
/// nothing) and mouse opted into drag-to-scroll, since the platform default
/// only allows touch/stylus/trackpad there.
class _VolumeTable extends StatefulWidget {
  final List<VolumeSummary> volumes;
  final void Function(VolumeSummary) onOpenDetail;
  final void Function(VolumeSummary) onDelete;

  const _VolumeTable({
    required this.volumes,
    required this.onOpenDetail,
    required this.onDelete,
  });

  @override
  State<_VolumeTable> createState() => _VolumeTableState();
}

class _VolumeTableState extends State<_VolumeTable> {
  final _verticalController = ScrollController();
  final _horizontalController = ScrollController();

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
                  DataColumn(label: Text('Size')),
                  DataColumn(label: Text('Containers')),
                  DataColumn(label: Text('Created')),
                  DataColumn(label: Text('Actions')),
                ],
                rows: widget.volumes.map(_buildRow).toList(),
              ),
            ),
          ),
        ),
      ),
    );
  }

  DataRow _buildRow(VolumeSummary volume) {
    return DataRow(
      onSelectChanged: (_) => widget.onOpenDetail(volume),
      cells: [
        DataCell(
          Icon(
            volume.orphaned ? Icons.help_outline : Icons.storage_outlined,
            size: 18,
            color: volume.orphaned ? Colors.orange : null,
          ),
        ),
        DataCell(Text(volume.name)),
        DataCell(Text(volume.serverName)),
        DataCell(Text(volume.driver)),
        DataCell(Text(formatBytes(volume.sizeBytes))),
        DataCell(Text('${volume.inUseBy.length}')),
        DataCell(
          Text(volume.createdAt?.toLocal().toString().split('.').first ?? '—'),
        ),
        DataCell(
          IconButton(
            icon: const Icon(Icons.delete_outline, size: 18),
            tooltip: 'Delete',
            visualDensity: VisualDensity.compact,
            color: Colors.red,
            onPressed: () => widget.onDelete(volume),
          ),
        ),
      ],
    );
  }
}
