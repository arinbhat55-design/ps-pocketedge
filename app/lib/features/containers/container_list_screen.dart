import 'dart:async';

import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import 'container_detail_screen.dart';

const _groupByLabels = {
  'none': 'None',
  'server': 'Server',
  'application': 'Application',
  'owner': 'Owner',
  'environment': 'Environment',
  'tag': 'Tag',
};

/// Fleet-wide container inventory: search + filter (server-side, since the
/// list spans every server) and client-side grouping by whichever
/// dimension is selected — every dimension a group could use is already
/// present on each [FleetContainer] the list endpoint returns.
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

  void _openDetail(FleetContainer c) {
    Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => ContainerDetailScreen(
        apiClient: widget.apiClient,
        serverId: c.serverId,
        serverName: c.serverName,
        container: c.container,
      ),
    ));
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Containers')),
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
                      child: Text('Failed to load containers: ${snapshot.error}'));
                }
                final containers = snapshot.data ?? [];

                final servers = <String, String>{
                  for (final c in containers) c.serverId: c.serverName,
                };
                final images = containers
                    .map((c) => c.container.image)
                    .whereType<String>()
                    .where((i) => i.isNotEmpty)
                    .toSet()
                    .toList()
                  ..sort();
                final statuses = containers.map((c) => c.container.state).toSet().toList()
                  ..sort();
                final owners = <String, String>{
                  for (final c in containers)
                    if (c.ownerId != null && c.ownerEmail != null) c.ownerId!: c.ownerEmail!,
                };
                final environments = containers
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
                      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
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
                              if (value != null) setState(() => _groupBy = value);
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
                                  ? containers.map(_ContainerTile.new).map(
                                      (tile) => tile.build(context, _openDetail)).toList()
                                  : [
                                      for (final key in groupKeys)
                                        ExpansionTile(
                                          title: Text(key),
                                          subtitle: Text('${grouped[key]!.length} container(s)'),
                                          initiallyExpanded: true,
                                          children: grouped[key]!
                                              .map(_ContainerTile.new)
                                              .map((tile) => tile.build(context, _openDetail))
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
}

class _ContainerTile {
  final FleetContainer fleetContainer;
  _ContainerTile(this.fleetContainer);

  Widget build(BuildContext context, void Function(FleetContainer) onTap) {
    final c = fleetContainer.container;
    return ListTile(
      dense: true,
      leading: Icon(Icons.circle, size: 10, color: containerStateColor(c.state)),
      title: Text(c.name),
      subtitle: Text(
        [
          fleetContainer.serverName,
          if (c.image != null && c.image!.isNotEmpty) c.image!,
          c.state,
        ].join(' • '),
      ),
      onTap: () => onTap(fleetContainer),
    );
  }
}
