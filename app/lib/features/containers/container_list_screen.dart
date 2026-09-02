import 'dart:async';

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
          _selectionMode ? '${_selectedKeys.length} selected' : 'Containers',
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
                          if (statuses.isNotEmpty) ...[
                            const SizedBox(width: 4),
                            const VerticalDivider(width: 1),
                            const SizedBox(width: 4),
                            ChoiceChip(
                              label: const Text('All statuses'),
                              selected: _selectedStatus == null,
                              onSelected: (_) {
                                setState(() => _selectedStatus = null);
                                _refresh();
                              },
                            ),
                            for (final status in statuses)
                              ChoiceChip(
                                label: Text(status),
                                selected: _selectedStatus == status,
                                onSelected: (_) {
                                  setState(() => _selectedStatus = status);
                                  _refresh();
                                },
                              ),
                          ],
                          if (images.isNotEmpty) ...[
                            const SizedBox(width: 4),
                            const VerticalDivider(width: 1),
                            const SizedBox(width: 4),
                            ChoiceChip(
                              label: const Text('All images'),
                              selected: _selectedImage == null,
                              onSelected: (_) {
                                setState(() => _selectedImage = null);
                                _refresh();
                              },
                            ),
                            for (final image in images)
                              ChoiceChip(
                                label: Text(image),
                                selected: _selectedImage == image,
                                onSelected: (_) {
                                  setState(() => _selectedImage = image);
                                  _refresh();
                                },
                              ),
                          ],
                          if (owners.isNotEmpty) ...[
                            const SizedBox(width: 4),
                            const VerticalDivider(width: 1),
                            const SizedBox(width: 4),
                            ChoiceChip(
                              label: const Text('All owners'),
                              selected: _selectedOwnerId == null,
                              onSelected: (_) {
                                setState(() => _selectedOwnerId = null);
                                _refresh();
                              },
                            ),
                            for (final entry in owners.entries)
                              ChoiceChip(
                                label: Text(entry.value),
                                selected: _selectedOwnerId == entry.key,
                                onSelected: (_) {
                                  setState(() => _selectedOwnerId = entry.key);
                                  _refresh();
                                },
                              ),
                          ],
                          if (environments.isNotEmpty) ...[
                            const SizedBox(width: 4),
                            const VerticalDivider(width: 1),
                            const SizedBox(width: 4),
                            ChoiceChip(
                              label: const Text('All environments'),
                              selected: _selectedEnvironment == null,
                              onSelected: (_) {
                                setState(() => _selectedEnvironment = null);
                                _refresh();
                              },
                            ),
                            for (final env in environments)
                              ChoiceChip(
                                label: Text(env),
                                selected: _selectedEnvironment == env,
                                onSelected: (_) {
                                  setState(() => _selectedEnvironment = env);
                                  _refresh();
                                },
                              ),
                          ],
                          if (tags.isNotEmpty) ...[
                            const SizedBox(width: 4),
                            const VerticalDivider(width: 1),
                            const SizedBox(width: 4),
                            ChoiceChip(
                              label: const Text('All tags'),
                              selected: _selectedTag == null,
                              onSelected: (_) {
                                setState(() => _selectedTag = null);
                                _refresh();
                              },
                            ),
                            for (final tag in tags)
                              ChoiceChip(
                                label: Text(tag),
                                selected: _selectedTag == tag,
                                onSelected: (_) {
                                  setState(() => _selectedTag = tag);
                                  _refresh();
                                },
                              ),
                          ],
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
                          : ListView(
                              padding: const EdgeInsets.symmetric(vertical: 4),
                              children: _groupBy == 'none'
                                  ? containers.map(_buildTile).toList()
                                  : [
                                      for (final key in groupKeys)
                                        ExpansionTile(
                                          title: Text(key),
                                          subtitle: Text(
                                            '${grouped[key]!.length} container(s)',
                                          ),
                                          initiallyExpanded: true,
                                          children: grouped[key]!
                                              .map(_buildTile)
                                              .toList(),
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

  Widget _buildTile(FleetContainer c) {
    final selected = _selectedKeys.contains(_selectionKey(c));
    return ListTile(
      dense: true,
      leading: _selectionMode
          ? Checkbox(value: selected, onChanged: (_) => _toggleSelected(c))
          : Icon(
              Icons.circle,
              size: 10,
              color: containerStateColor(c.container.state),
            ),
      title: Text(c.container.name),
      subtitle: Text(
        [
          c.serverName,
          if (c.container.image != null && c.container.image!.isNotEmpty)
            c.container.image!,
          c.container.state,
        ].join(' • '),
      ),
      onTap: _selectionMode ? () => _toggleSelected(c) : () => _openDetail(c),
      onLongPress: () {
        if (!_selectionMode) setState(() => _selectionMode = true);
        _toggleSelected(c);
      },
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
