import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import 'container_detail_screen.dart';
import 'create_container_dialog.dart';

const _groupByLabels = {
  'none': 'None',
  'server': 'Server',
  'application': 'Application',
  'owner': 'Owner',
  'environment': 'Environment',
  'tag': 'Tag',
};

/// One row's unique key across the fleet — containerId alone isn't
/// guaranteed unique across different servers/Docker daemons.
String _selectionKey(FleetContainer c) =>
    '${c.serverId}:${c.container.containerId}';

/// Fleet-wide container inventory: search + filter (server-side, since the
/// list spans every server), client-side grouping by whichever dimension
/// is selected, a multi-select mode for bulk start/stop/restart/remove
/// across servers, and a "Create container" entry point.
class ContainerListScreen extends StatefulWidget {
  final ApiClient apiClient;

  const ContainerListScreen({super.key, required this.apiClient});

  @override
  State<ContainerListScreen> createState() => _ContainerListScreenState();
}

class _ContainerListScreenState extends State<ContainerListScreen> {
  late Future<List<FleetContainer>> _containersFuture;
  final _searchController = TextEditingController();
  Timer? _debounce;

  String? _selectedServerId;
  String? _selectedImage;
  String? _selectedStatus;
  String? _selectedOwnerId;
  String? _selectedEnvironment;
  String? _selectedTag;
  String _groupBy = 'none';

  bool _selectionMode = false;
  final Set<String> _selectedKeys = {};
  // Populated on every load so bulk actions can map a selected key back to
  // its {serverId, containerId} without re-scanning the current snapshot.
  Map<String, FleetContainer> _byKey = {};
  bool _bulkRunning = false;

  @override
  void initState() {
    super.initState();
    _containersFuture = _load();
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _searchController.dispose();
    super.dispose();
  }

  Future<List<FleetContainer>> _load() {
    return widget.apiClient.listContainers(
      name: _searchController.text.trim().isEmpty
          ? null
          : _searchController.text.trim(),
      serverId: _selectedServerId,
      image: _selectedImage,
      status: _selectedStatus,
      ownerId: _selectedOwnerId,
      environment: _selectedEnvironment,
      tags: _selectedTag == null ? null : [_selectedTag!],
    );
  }

  void _refresh() {
    setState(() => _containersFuture = _load());
  }

  void _onSearchChanged(String _) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 300), _refresh);
  }

  Map<String, List<FleetContainer>> _grouped(List<FleetContainer> containers) {
    if (_groupBy == 'none') return {'': containers};

    final groups = <String, List<FleetContainer>>{};
    void add(String key, FleetContainer c) {
      groups.putIfAbsent(key, () => []).add(c);
    }

    for (final c in containers) {
      switch (_groupBy) {
        case 'server':
          add(c.serverName, c);
        case 'application':
          add(c.stackName ?? 'Unmanaged', c);
        case 'owner':
          add(c.ownerEmail ?? 'Unowned', c);
        case 'environment':
          add(c.environment ?? 'No environment', c);
        case 'tag':
          if (c.tags.isEmpty) {
            add('Untagged', c);
          } else {
            for (final tag in c.tags) {
              add(tag, c);
            }
          }
        default:
          add('', c);
      }
    }
    return groups;
  }

  Future<void> _openDetail(FleetContainer c) async {
    final changed = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => ContainerDetailScreen(
          apiClient: widget.apiClient,
          serverId: c.serverId,
          serverName: c.serverName,
          container: c.container,
        ),
      ),
    );
    if (changed == true) _refresh();
  }

  Future<void> _openCreateContainer() async {
    final newId = await showCreateContainerDialog(
      context,
      apiClient: widget.apiClient,
    );
    if (newId != null) _refresh();
  }

  void _toggleSelectionMode() {
    setState(() {
      _selectionMode = !_selectionMode;
      if (!_selectionMode) _selectedKeys.clear();
    });
  }

  void _toggleSelected(FleetContainer c) {
    setState(() {
      final key = _selectionKey(c);
      if (_selectedKeys.contains(key)) {
        _selectedKeys.remove(key);
      } else {
        _selectedKeys.add(key);
      }
    });
  }

  Future<void> _runBulkAction(String action) async {
    final targets = [
      for (final key in _selectedKeys)
        if (_byKey[key] != null)
          BulkActionTarget(
            serverId: _byKey[key]!.serverId,
            containerId: _byKey[key]!.container.containerId,
          ),
    ];
    if (targets.isEmpty) return;

    if (action == 'remove') {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (_) => AlertDialog(
          title: Text('Remove ${targets.length} container(s)?'),
          content: const Text(
            'This permanently deletes them, stopping any that are still running. '
            'Any data outside a named volume is lost.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Cancel'),
            ),
            FilledButton(
              style: FilledButton.styleFrom(backgroundColor: Colors.red),
              onPressed: () => Navigator.of(context).pop(true),
              child: const Text('Remove'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
    }

    setState(() => _bulkRunning = true);
    try {
      final results = await widget.apiClient.bulkContainerAction(
        targets,
        action,
        force: action == 'remove',
      );
      final succeeded = results.where((r) => r.success).length;
      final failed = results.length - succeeded;
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              '$action: $succeeded succeeded'
              '${failed > 0 ? ', $failed failed' : ''}',
            ),
          ),
        );
      }
      setState(() {
        _selectionMode = false;
        _selectedKeys.clear();
      });
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Bulk $action failed: $e')));
      }
    } finally {
      if (mounted) setState(() => _bulkRunning = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          _selectionMode
              ? '${_selectedKeys.length} selected'
              : 'Container Management',
        ),
        actions: [
          IconButton(
            icon: Icon(_selectionMode ? Icons.close : Icons.checklist),
            tooltip: _selectionMode ? 'Cancel selection' : 'Select',
            onPressed: _toggleSelectionMode,
          ),
        ],
      ),
      floatingActionButton: _selectionMode
          ? null
          : FloatingActionButton.extended(
              onPressed: _openCreateContainer,
              icon: const Icon(Icons.add),
              label: const Text('Create container'),
            ),
      bottomNavigationBar: _selectionMode && _selectedKeys.isNotEmpty
          ? _BulkActionBar(running: _bulkRunning, onAction: _runBulkAction)
          : null,
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 0),
            child: TextField(
              controller: _searchController,
              onChanged: _onSearchChanged,
              decoration: const InputDecoration(
                hintText: 'Search by container name',
                prefixIcon: Icon(Icons.search),
                isDense: true,
                border: OutlineInputBorder(),
              ),
            ),
          ),
          Expanded(
            child: FutureBuilder<List<FleetContainer>>(
              future: _containersFuture,
              builder: (context, snapshot) {
                if (snapshot.connectionState == ConnectionState.waiting) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError) {
                  return Center(
                    child: Text('Failed to load containers: ${snapshot.error}'),
                  );
                }
                final containers = snapshot.data ?? [];
                _byKey = {for (final c in containers) _selectionKey(c): c};

                final servers = <String, String>{
                  for (final c in containers) c.serverId: c.serverName,
                };
                final images =
                    containers
                        .map((c) => c.container.image)
                        .whereType<String>()
                        .where((i) => i.isNotEmpty)
                        .toSet()
                        .toList()
                      ..sort();
                final statuses =
                    containers.map((c) => c.container.state).toSet().toList()
                      ..sort();
                final owners = <String, String>{
                  for (final c in containers)
                    if (c.ownerId != null && c.ownerEmail != null)
                      c.ownerId!: c.ownerEmail!,
                };
                final environments =
                    containers
                        .map((c) => c.environment)
                        .whereType<String>()
                        .toSet()
                        .toList()
                      ..sort();
                final tags = containers.expand((c) => c.tags).toSet().toList()
                  ..sort();

                final grouped = _grouped(containers);
                final groupKeys = grouped.keys.toList()..sort();

                return Column(
                  children: [
                    Padding(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 12,
                        vertical: 8,
                      ),
                      child: Wrap(
                        spacing: 16,
                        runSpacing: 8,
                        crossAxisAlignment: WrapCrossAlignment.center,
                        children: [
                          _filterDropdown(
                            label: 'Server',
                            value: _selectedServerId,
                            options: servers,
                            onChanged: (v) {
                              setState(() => _selectedServerId = v);
                              _refresh();
                            },
                          ),
                          if (statuses.isNotEmpty)
                            _filterDropdown(
                              label: 'Status',
                              value: _selectedStatus,
                              options: {for (final s in statuses) s: s},
                              onChanged: (v) {
                                setState(() => _selectedStatus = v);
                                _refresh();
                              },
                            ),
                          if (images.isNotEmpty)
                            _filterDropdown(
                              label: 'Image',
                              value: _selectedImage,
                              options: {for (final i in images) i: i},
                              onChanged: (v) {
                                setState(() => _selectedImage = v);
                                _refresh();
                              },
                            ),
                          if (owners.isNotEmpty)
                            _filterDropdown(
                              label: 'Owner',
                              value: _selectedOwnerId,
                              options: owners,
                              onChanged: (v) {
                                setState(() => _selectedOwnerId = v);
                                _refresh();
                              },
                            ),
                          if (environments.isNotEmpty)
                            _filterDropdown(
                              label: 'Environment',
                              value: _selectedEnvironment,
                              options: {for (final e in environments) e: e},
                              onChanged: (v) {
                                setState(() => _selectedEnvironment = v);
                                _refresh();
                              },
                            ),
                          if (tags.isNotEmpty)
                            _filterDropdown(
                              label: 'Tag',
                              value: _selectedTag,
                              options: {for (final t in tags) t: t},
                              onChanged: (v) {
                                setState(() => _selectedTag = v);
                                _refresh();
                              },
                            ),
                        ],
                      ),
                    ),
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 12),
                      child: Row(
                        children: [
                          const Text('Group by:'),
                          const SizedBox(width: 8),
                          DropdownButton<String>(
                            value: _groupBy,
                            items: [
                              for (final entry in _groupByLabels.entries)
                                DropdownMenuItem(
                                  value: entry.key,
                                  child: Text(entry.value),
                                ),
                            ],
                            onChanged: (value) {
                              if (value != null)
                                setState(() => _groupBy = value);
                            },
                          ),
                        ],
                      ),
                    ),
                    Expanded(
                      child: containers.isEmpty
                          ? const Center(child: Text('No containers found.'))
                          : _groupBy == 'none'
                          ? SingleChildScrollView(
                              padding: const EdgeInsets.symmetric(
                                vertical: 4,
                              ),
                              child: _buildTable(containers),
                            )
                          : ListView(
                              padding: const EdgeInsets.symmetric(vertical: 4),
                              children: [
                                for (final key in groupKeys)
                                  ExpansionTile(
                                    title: Text(key),
                                    subtitle: Text(
                                      '${grouped[key]!.length} container(s)',
                                    ),
                                    initiallyExpanded: true,
                                    children: [_buildTable(grouped[key]!)],
                                  ),
                              ],
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

  /// A compact "Label: value" dropdown filter — replaces what used to be a
  /// row of one chip per distinct value, which got unreadable once there
  /// were more than a handful of servers/images/etc.
  Widget _filterDropdown({
    required String label,
    required String? value,
    required Map<String, String> options,
    required ValueChanged<String?> onChanged,
  }) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text('$label:'),
        const SizedBox(width: 6),
        DropdownButtonHideUnderline(
          child: DropdownButton<String?>(
            value: value,
            isDense: true,
            items: [
              const DropdownMenuItem<String?>(value: null, child: Text('All')),
              for (final entry in options.entries)
                DropdownMenuItem<String?>(
                  value: entry.key,
                  child: Text(entry.value),
                ),
            ],
            onChanged: onChanged,
          ),
        ),
      ],
    );
  }

  String _formatPorts(FleetContainer c) {
    if (c.container.ports.isEmpty) return '—';
    return c.container.ports
        .map((p) {
          final proto = p.type.isEmpty ? 'tcp' : p.type;
          return p.publicPort != 0
              ? '${p.publicPort}:${p.privatePort}/$proto'
              : '${p.privatePort}/$proto';
        })
        .join(', ');
  }

  Future<void> _runRowAction(FleetContainer c, String action) async {
    if (action == 'remove') {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (_) => AlertDialog(
          title: const Text('Remove container?'),
          content: const Text(
            'This permanently deletes the container, stopping it first if '
            'running. Any data outside a named volume is lost.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Cancel'),
            ),
            FilledButton(
              style: FilledButton.styleFrom(backgroundColor: Colors.red),
              onPressed: () => Navigator.of(context).pop(true),
              child: const Text('Remove'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
    }

    try {
      final result = await widget.apiClient.containerAction(
        c.serverId,
        c.container.containerId,
        action,
        force: action == 'remove',
      );
      if (!mounted) return;
      if (!result.success) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(result.error ?? '$action failed')));
        return;
      }
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to $action container: $e')));
      }
    }
  }

  Widget _buildTable(List<FleetContainer> list) {
    return _ContainerTable(
      containers: list,
      selectionMode: _selectionMode,
      selectedKeys: _selectedKeys,
      onToggleSelected: _toggleSelected,
      onOpenDetail: _openDetail,
      onRunAction: _runRowAction,
      formatPorts: _formatPorts,
    );
  }
}

/// One fleet-container table: an [Axis.horizontal]-scrolling [DataTable]
/// with a dedicated [ScrollController]. This has to be its own
/// StatefulWidget (rather than a method on [_ContainerListScreenState])
/// because [Scrollbar.thumbVisibility] requires a real ScrollController —
/// without one it silently fails to attach, so the thumb paints but
/// dragging it does nothing — and grouped mode renders multiple tables at
/// once, which a single shared controller can't drive independently. It
/// also opts the table's ScrollConfiguration into mouse drag-to-scroll,
/// since the platform default only allows touch/stylus/trackpad (mouse
/// drag is reserved for text selection), leaving mouse users only the
/// wheel to scroll otherwise.
class _ContainerTable extends StatefulWidget {
  final List<FleetContainer> containers;
  final bool selectionMode;
  final Set<String> selectedKeys;
  final void Function(FleetContainer) onToggleSelected;
  final void Function(FleetContainer) onOpenDetail;
  final Future<void> Function(FleetContainer, String) onRunAction;
  final String Function(FleetContainer) formatPorts;

  const _ContainerTable({
    required this.containers,
    required this.selectionMode,
    required this.selectedKeys,
    required this.onToggleSelected,
    required this.onOpenDetail,
    required this.onRunAction,
    required this.formatPorts,
  });

  @override
  State<_ContainerTable> createState() => _ContainerTableState();
}

class _ContainerTableState extends State<_ContainerTable> {
  final _scrollController = ScrollController();

  @override
  void dispose() {
    _scrollController.dispose();
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
        controller: _scrollController,
        thumbVisibility: true,
        trackVisibility: true,
        child: SingleChildScrollView(
          controller: _scrollController,
          scrollDirection: Axis.horizontal,
          padding: const EdgeInsets.only(bottom: 12),
          child: DataTable(
            showCheckboxColumn: widget.selectionMode,
            columns: const [
              DataColumn(label: Text('Status')),
              DataColumn(label: Text('Name')),
              DataColumn(label: Text('Container ID')),
              DataColumn(label: Text('Image')),
              DataColumn(label: Text('Ports')),
              DataColumn(label: Text('Server')),
              DataColumn(label: Text('State')),
              DataColumn(label: Text('Actions')),
            ],
            rows: widget.containers.map(_buildRow).toList(),
          ),
        ),
      ),
    );
  }

  DataRow _buildRow(FleetContainer c) {
    final selected = widget.selectedKeys.contains(_selectionKey(c));
    final id = c.container.containerId;
    final shortId = id.length > 12 ? id.substring(0, 12) : id;

    return DataRow(
      selected: selected,
      onSelectChanged: (_) {
        if (widget.selectionMode) {
          widget.onToggleSelected(c);
        } else {
          widget.onOpenDetail(c);
        }
      },
      cells: [
        DataCell(
          Icon(
            Icons.circle,
            size: 10,
            color: containerStateColor(c.container.state),
          ),
        ),
        DataCell(Text(c.container.name)),
        DataCell(
          Text(shortId, style: const TextStyle(fontFamily: 'monospace')),
        ),
        DataCell(Text(c.container.image ?? '—')),
        DataCell(Text(widget.formatPorts(c))),
        DataCell(Text(c.serverName)),
        DataCell(Text(c.container.state)),
        DataCell(_buildRowActions(c)),
      ],
    );
  }

  Widget _buildRowActions(FleetContainer c) {
    final running = c.container.state == 'running';
    final paused = c.container.state == 'paused';
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        IconButton(
          icon: const Icon(Icons.play_arrow, size: 18),
          tooltip: 'Start',
          visualDensity: VisualDensity.compact,
          onPressed: running ? null : () => widget.onRunAction(c, 'start'),
        ),
        IconButton(
          icon: const Icon(Icons.stop, size: 18),
          tooltip: 'Stop',
          visualDensity: VisualDensity.compact,
          onPressed: running ? () => widget.onRunAction(c, 'stop') : null,
        ),
        IconButton(
          icon: Icon(
            paused ? Icons.play_circle_outline : Icons.pause_circle_outline,
            size: 18,
          ),
          tooltip: paused ? 'Resume' : 'Pause',
          visualDensity: VisualDensity.compact,
          onPressed: running || paused
              ? () => widget.onRunAction(c, paused ? 'resume' : 'pause')
              : null,
        ),
        IconButton(
          icon: const Icon(Icons.delete_outline, size: 18),
          tooltip: 'Remove',
          visualDensity: VisualDensity.compact,
          color: Colors.red,
          onPressed: () => widget.onRunAction(c, 'remove'),
        ),
      ],
    );
  }
}

class _BulkActionBar extends StatelessWidget {
  final bool running;
  final void Function(String action) onAction;

  const _BulkActionBar({required this.running, required this.onAction});

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: running
            ? const Center(
                child: SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              )
            : Wrap(
                alignment: WrapAlignment.center,
                spacing: 8,
                children: [
                  FilledButton.tonalIcon(
                    onPressed: () => onAction('start'),
                    icon: const Icon(Icons.play_arrow, size: 18),
                    label: const Text('Start'),
                  ),
                  FilledButton.tonalIcon(
                    onPressed: () => onAction('stop'),
                    icon: const Icon(Icons.stop, size: 18),
                    label: const Text('Stop'),
                  ),
                  FilledButton.tonalIcon(
                    onPressed: () => onAction('restart'),
                    icon: const Icon(Icons.refresh, size: 18),
                    label: const Text('Restart'),
                  ),
                  FilledButton.tonalIcon(
                    onPressed: () => onAction('pause'),
                    icon: const Icon(Icons.pause_circle_outline, size: 18),
                    label: const Text('Pause'),
                  ),
                  FilledButton.tonalIcon(
                    onPressed: () => onAction('resume'),
                    icon: const Icon(Icons.play_circle_outline, size: 18),
                    label: const Text('Resume'),
                  ),
                  FilledButton.icon(
                    style: FilledButton.styleFrom(backgroundColor: Colors.red),
                    onPressed: () => onAction('remove'),
                    icon: const Icon(Icons.delete, size: 18),
                    label: const Text('Remove'),
                  ),
                ],
              ),
      ),
    );
  }
}
