import 'dart:async';

import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import '../../models/server.dart';
import '../../widgets/formatting.dart';
import '../../widgets/notice_banner.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';
import 'container_detail_screen.dart';
import 'container_table.dart';
import 'create_container_dialog.dart';
import '../../theme/app_theme.dart';

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
  final bool isAdmin;

  const ContainerListScreen({
    super.key,
    required this.apiClient,
    this.isAdmin = true,
  });

  @override
  State<ContainerListScreen> createState() => _ContainerListScreenState();
}

class _ContainerListScreenState extends State<ContainerListScreen> {
  late Future<List<FleetContainer>> _containersFuture;
  final _searchController = TextEditingController();
  Timer? _debounce;
  // Agents report every 20 s; polling at half that keeps the list close to
  // live without a stream per container.
  Timer? _poll;

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

  // Filter dropdown choices, built from an unfiltered listing so they don't
  // shrink (or lose the selected value) when a search/filter narrows the
  // results. Reloaded only when containers change, not on filter changes.
  _FilterOptions _options = const _FilterOptions();
  // The unfiltered listing and each server's connectivity, so a container
  // on a server whose agent has gone quiet isn't shown as live state.
  List<FleetContainer> _all = const [];
  Map<String, Server> _servers = const {};
  // Phones collapse the filter dropdowns behind a "Filters" toggle.
  bool _filtersExpanded = false;

  @override
  void initState() {
    super.initState();
    _containersFuture = _load();
    _loadOptions();
    _poll = Timer.periodic(kListPollInterval, (_) {
      // Skip while this tab is hidden in the shell's IndexedStack, or
      // while a bulk action is mid-flight.
      final visible = mounted && TickerMode.valuesOf(context).enabled;
      if (visible && !_bulkRunning) _refresh(reloadOptions: true);
    });
  }

  @override
  void dispose() {
    _poll?.cancel();
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

  /// Reloads the (filtered) list; [reloadOptions] also refreshes the filter
  /// choices, for when containers were created/removed/changed state rather
  /// than just the filters changing.
  void _refresh({bool reloadOptions = false}) {
    setState(() {
      _containersFuture = _load();
    });
    if (reloadOptions) _loadOptions();
  }

  Future<void> _loadOptions() async {
    try {
      final all = await widget.apiClient.listContainers();
      if (!mounted) return;
      setState(() {
        _all = all;
        _options = _FilterOptions.from(all);
      });
    } catch (_) {
      // Non-fatal: the dropdowns still offer "All" plus whatever's
      // currently selected, and the list itself reports load errors.
    }
    try {
      final servers = await widget.apiClient.listServers();
      if (mounted) {
        setState(() => _servers = {for (final s in servers) s.id: s});
      }
    } catch (_) {
      // Without server info every container is treated as reachable,
      // which is how the list behaved before connectivity was shown.
    }
  }

  /// What the list shows for [c]: its status pill, a line of detail, and
  /// whether its server can currently act on it. A container on a
  /// disconnected server shows "Unknown" plus the last reported state,
  /// rather than a confident "Running" from a stale snapshot.
  ContainerRowView _view(FleetContainer c) {
    final server = _servers[c.serverId];
    final disconnected =
        server != null && serverStatus(server).tone == StatusTone.failed;
    if (disconnected) {
      final last = containerStatus(c.container.state).label.toLowerCase();
      final seen = server.lastHeartbeatAt;
      return (
        status: (label: 'Unknown', tone: StatusTone.neutral),
        detail: seen == null
            ? 'Server hasn\'t reported yet'
            : 'Was $last · ${formatAgo(seen)}',
        reachable: false,
      );
    }
    final status = c.container.status;
    return (
      status: containerStatusDetailed(c.container.state, status),
      detail: status == null || status.isEmpty ? null : status,
      reachable: true,
    );
  }

  int get _activeFilterCount => [
    _selectedServerId,
    _selectedImage,
    _selectedStatus,
    _selectedOwnerId,
    _selectedEnvironment,
    _selectedTag,
  ].where((v) => v != null).length;

  bool get _hasActiveFilters =>
      _activeFilterCount > 0 || _searchController.text.trim().isNotEmpty;

  void _clearFilters() {
    _debounce?.cancel();
    _searchController.clear();
    setState(() {
      _selectedServerId = null;
      _selectedImage = null;
      _selectedStatus = null;
      _selectedOwnerId = null;
      _selectedEnvironment = null;
      _selectedTag = null;
    });
    _refresh();
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

  Future<void> _openDetail(FleetContainer c, {int initialTab = 0}) async {
    final changed = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => ContainerDetailScreen(
          apiClient: widget.apiClient,
          serverId: c.serverId,
          serverName: c.serverName,
          container: c.container,
          initialTab: initialTab,
          isAdmin: widget.isAdmin,
        ),
      ),
    );
    if (changed == true) _refresh(reloadOptions: true);
  }

  Future<void> _openCreateContainer() async {
    final newId = await showCreateContainerDialog(
      context,
      apiClient: widget.apiClient,
    );
    if (newId != null) _refresh(reloadOptions: true);
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
              style: FilledButton.styleFrom(backgroundColor: AppColors.failed),
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
      _refresh(reloadOptions: true);
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
    final compact = isCompactWidth(context);
    final create = PrimaryAction(
      label: 'Create container',
      icon: Icons.add,
      onPressed: _openCreateContainer,
    );
    return Scaffold(
      appBar: AppBar(
        title: Text(
          _selectionMode ? '${_selectedKeys.length} selected' : 'Containers',
        ),
        actions: [
          if (!_selectionMode)
            IconButton(
              icon: const Icon(Icons.refresh),
              tooltip: 'Refresh',
              onPressed: () => _refresh(reloadOptions: true),
            ),
          if (widget.isAdmin)
            IconButton(
              icon: Icon(_selectionMode ? Icons.close : Icons.checklist),
              tooltip: _selectionMode ? 'Cancel selection' : 'Select',
              onPressed: _toggleSelectionMode,
            ),
          if (widget.isAdmin && !_selectionMode) ?create.appBarAction(context),
        ],
      ),
      floatingActionButton: !widget.isAdmin || _selectionMode
          ? null
          : create.fab(context),
      bottomNavigationBar: _selectionMode && _selectedKeys.isNotEmpty
          ? _BulkActionBar(running: _bulkRunning, onAction: _runBulkAction)
          : null,
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          PageIntro(
            description:
                'Every container across your servers. Search and filter, '
                'or select several to act on them together.',
            summary: _all.isEmpty ? const [] : _summary(),
          ),
          if (_unknownCount > 0)
            Padding(
              padding: const EdgeInsets.fromLTRB(
                Space.lg,
                Space.md,
                Space.lg,
                0,
              ),
              child: NoticeBanner(
                tone: StatusTone.warning,
                icon: Icons.link_off,
                title: _unknownCount == 1
                    ? '1 container is on a disconnected server'
                    : '$_unknownCount containers are on a disconnected server',
                message:
                    'Their status is from the server\'s last report and may '
                    'be out of date. Actions are unavailable until its agent '
                    'reconnects.',
              ),
            ),
          Padding(
            padding: const EdgeInsets.fromLTRB(Space.lg, Space.md, Space.lg, 0),
            child: TextField(
              controller: _searchController,
              onChanged: (v) {
                // Rebuild now so the clear icon / "Clear filters" appear
                // immediately; the reload itself is debounced.
                setState(() {});
                _onSearchChanged(v);
              },
              decoration: InputDecoration(
                hintText: 'Search by container name',
                prefixIcon: const Icon(Icons.search),
                suffixIcon: _searchController.text.isEmpty
                    ? null
                    : IconButton(
                        icon: const Icon(Icons.clear),
                        tooltip: 'Clear search',
                        onPressed: () {
                          _debounce?.cancel();
                          _searchController.clear();
                          _refresh();
                        },
                      ),
                isDense: true,
                border: const OutlineInputBorder(),
              ),
            ),
          ),
          _buildFilterBar(compact),
          Expanded(
            child: FutureBuilder<List<FleetContainer>>(
              future: _containersFuture,
              builder: (context, snapshot) {
                if (snapshot.connectionState == ConnectionState.waiting &&
                    !snapshot.hasData) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError) {
                  return StateMessage.error(
                    what: 'containers',
                    error: snapshot.error,
                    onRetry: () => _refresh(reloadOptions: true),
                  );
                }
                final containers = snapshot.data ?? [];
                _byKey = {for (final c in containers) _selectionKey(c): c};

                if (containers.isEmpty) {
                  return _hasActiveFilters
                      ? StateMessage(
                          icon: Icons.filter_alt_off_outlined,
                          title: 'No containers match',
                          message:
                              'Nothing matches the current search and '
                              'filters. Clear them to see every container.',
                          actionLabel: 'Clear filters',
                          actionIcon: Icons.filter_alt_off,
                          onAction: _clearFilters,
                        )
                      : StateMessage(
                          icon: Icons.view_in_ar_outlined,
                          title: 'No containers yet',
                          message:
                              'Containers running on your enrolled servers '
                              'show up here. Create one, or deploy an app '
                              'from the Servers tab.',
                          actionLabel: widget.isAdmin
                              ? 'Create container'
                              : null,
                          actionIcon: widget.isAdmin ? Icons.add : null,
                          onAction: widget.isAdmin
                              ? _openCreateContainer
                              : null,
                        );
                }

                final grouped = _grouped(containers);
                final groupKeys = grouped.keys.toList()..sort();

                return LayoutBuilder(
                  builder: (context, constraints) {
                    final cards = constraints.maxWidth < kTableMinWidth;
                    // Leave room for the FAB under the last row.
                    const listPadding = EdgeInsets.only(top: 4, bottom: 88);

                    if (!cards) {
                      return _buildTable([
                        if (_groupBy == 'none')
                          (name: '', containers: containers)
                        else
                          for (final key in groupKeys)
                            (name: key, containers: grouped[key]!),
                      ]);
                    }
                    if (_groupBy == 'none') {
                      return ListView.builder(
                        padding: listPadding,
                        itemCount: containers.length,
                        itemBuilder: (_, i) => _buildCard(containers[i]),
                      );
                    }
                    return ListView(
                      padding: listPadding,
                      children: [
                        for (final key in groupKeys)
                          ExpansionTile(
                            title: Text(key),
                            subtitle: Text(
                              '${grouped[key]!.length} container(s)',
                            ),
                            initiallyExpanded: true,
                            children: [
                              for (final c in grouped[key]!) _buildCard(c),
                            ],
                          ),
                      ],
                    );
                  },
                );
              },
            ),
          ),
        ],
      ),
    );
  }

  int get _unknownCount => _all.where((c) => !_view(c).reachable).length;

  /// Counts by what each container's pill says, so the summary and the
  /// list never disagree.
  List<Widget> _summary() {
    final counts = <StatusTone, int>{};
    for (final c in _all) {
      final tone = _view(c).status.tone;
      counts[tone] = (counts[tone] ?? 0) + 1;
    }
    final running = _all
        .where((c) => _view(c).status.label == 'Running')
        .length;
    final unknown = _unknownCount;
    final attention = counts[StatusTone.warning] ?? 0;
    final failed = counts[StatusTone.failed] ?? 0;
    final stopped = _all
        .where((c) => _view(c).status.label == 'Stopped')
        .length;
    return [
      SummaryStat(
        value: '$running',
        label: 'running',
        color: running > 0 ? AppColors.healthy : null,
      ),
      if (attention > 0)
        SummaryStat(
          value: '$attention',
          label: 'need attention',
          color: AppColors.warning,
        ),
      SummaryStat(value: '$stopped', label: 'stopped'),
      if (failed > 0)
        SummaryStat(value: '$failed', label: 'failed', color: AppColors.failed),
      if (unknown > 0) SummaryStat(value: '$unknown', label: 'unknown'),
      SummaryStat(value: '${_all.length}', label: 'total'),
    ];
  }

  /// Filter dropdowns + group-by + "Clear filters". Built from [_options]
  /// (not the current results) so it stays put while the list reloads.
  /// On phones the dropdowns collapse behind a "Filters" toggle so they
  /// don't push the list off-screen.
  Widget _buildFilterBar(bool compact) {
    final controls = <Widget>[
      _filterDropdown(
        label: 'Server',
        value: _selectedServerId,
        options: _options.servers,
        onChanged: (v) {
          setState(() => _selectedServerId = v);
          _refresh();
        },
      ),
      _filterDropdown(
        label: 'Status',
        value: _selectedStatus,
        options: {for (final s in _options.statuses) s: s},
        onChanged: (v) {
          setState(() => _selectedStatus = v);
          _refresh();
        },
      ),
      if (_options.images.isNotEmpty || _selectedImage != null)
        _filterDropdown(
          label: 'Image',
          value: _selectedImage,
          options: {for (final i in _options.images) i: i},
          onChanged: (v) {
            setState(() => _selectedImage = v);
            _refresh();
          },
        ),
      if (_options.owners.isNotEmpty || _selectedOwnerId != null)
        _filterDropdown(
          label: 'Owner',
          value: _selectedOwnerId,
          options: _options.owners,
          onChanged: (v) {
            setState(() => _selectedOwnerId = v);
            _refresh();
          },
        ),
      if (_options.environments.isNotEmpty || _selectedEnvironment != null)
        _filterDropdown(
          label: 'Environment',
          value: _selectedEnvironment,
          options: {for (final e in _options.environments) e: e},
          onChanged: (v) {
            setState(() => _selectedEnvironment = v);
            _refresh();
          },
        ),
      if (_options.tags.isNotEmpty || _selectedTag != null)
        _filterDropdown(
          label: 'Tag',
          value: _selectedTag,
          options: {for (final t in _options.tags) t: t},
          onChanged: (v) {
            setState(() => _selectedTag = v);
            _refresh();
          },
        ),
      Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Text('Group by:'),
          const SizedBox(width: 6),
          DropdownButtonHideUnderline(
            child: DropdownButton<String>(
              value: _groupBy,
              isDense: true,
              items: [
                for (final entry in _groupByLabels.entries)
                  DropdownMenuItem(value: entry.key, child: Text(entry.value)),
              ],
              onChanged: (value) {
                if (value != null) setState(() => _groupBy = value);
              },
            ),
          ),
        ],
      ),
    ];

    final clearButton = _hasActiveFilters
        ? TextButton.icon(
            onPressed: _clearFilters,
            icon: const Icon(Icons.filter_alt_off, size: 18),
            label: const Text('Clear filters'),
          )
        : null;

    if (!compact) {
      return Padding(
        padding: const EdgeInsets.fromLTRB(
          Space.lg,
          Space.md,
          Space.lg,
          Space.xs,
        ),
        child: Wrap(
          spacing: 16,
          runSpacing: 8,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [...controls, ?clearButton],
        ),
      );
    }

    final count = _activeFilterCount;
    return Padding(
      padding: const EdgeInsets.fromLTRB(Space.sm, Space.xs, Space.sm, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              TextButton.icon(
                onPressed: () =>
                    setState(() => _filtersExpanded = !_filtersExpanded),
                icon: Icon(
                  _filtersExpanded ? Icons.expand_less : Icons.tune,
                  size: 18,
                ),
                label: Text(count > 0 ? 'Filters ($count)' : 'Filters'),
              ),
              const Spacer(),
              ?clearButton,
            ],
          ),
          if (_filtersExpanded)
            Padding(
              padding: const EdgeInsets.fromLTRB(
                Space.sm,
                0,
                Space.sm,
                Space.sm,
              ),
              child: Wrap(
                spacing: 16,
                runSpacing: 8,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: controls,
              ),
            ),
        ],
      ),
    );
  }

  /// A compact "Label: value" dropdown filter — replaces what used to be a
  /// row of one chip per distinct value, which got unreadable once there
  /// were more than a handful of servers/images/etc. The current [value]
  /// is always offered even if [options] no longer contains it (e.g. the
  /// last container with that image was removed), since a
  /// [DropdownButton] asserts that its value is among its items.
  Widget _filterDropdown({
    required String label,
    required String? value,
    required Map<String, String> options,
    required ValueChanged<String?> onChanged,
  }) {
    final items = {
      ...options,
      if (value != null && !options.containsKey(value)) value: value,
    };
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
              for (final entry in items.entries)
                DropdownMenuItem<String?>(
                  value: entry.key,
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 220),
                    child: Text(entry.value, overflow: TextOverflow.ellipsis),
                  ),
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
              style: FilledButton.styleFrom(backgroundColor: AppColors.failed),
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
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(result.error ?? '$action failed')),
        );
        return;
      }
      _refresh(reloadOptions: true);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to $action container: $e')),
        );
      }
    }
  }

  /// Desktop table in a bordered panel. Checkboxes are always visible
  /// there, so selecting from the table enters/leaves selection mode by
  /// itself.
  Widget _buildTable(List<ContainerGroup> groups) {
    void syncMode() => _selectionMode = _selectedKeys.isNotEmpty;
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        Space.lg,
        Space.xs,
        Space.lg,
        Space.lg,
      ),
      child: DecoratedBox(
        decoration: BoxDecoration(
          border: Border.all(
            color: Theme.of(context).colorScheme.outlineVariant,
          ),
          borderRadius: BorderRadius.circular(Radii.md),
        ),
        child: ClipRRect(
          borderRadius: BorderRadius.circular(Radii.md),
          child: ContainerTable(
            groups: groups,
            readOnly: !widget.isAdmin,
            selectedKeys: _selectedKeys,
            keyOf: _selectionKey,
            viewOf: _view,
            onToggleSelected: (c) => setState(() {
              final key = _selectionKey(c);
              if (!_selectedKeys.remove(key)) _selectedKeys.add(key);
              syncMode();
            }),
            onSetSelected: (list, select) => setState(() {
              for (final c in list) {
                select
                    ? _selectedKeys.add(_selectionKey(c))
                    : _selectedKeys.remove(_selectionKey(c));
              }
              syncMode();
            }),
            onOpen: _openDetail,
            onAction: (c, action) => switch (action) {
              ContainerRowAction.details => _openDetail(c),
              ContainerRowAction.logs => _openDetail(c, initialTab: 2),
              ContainerRowAction.terminal => _openDetail(c, initialTab: 3),
              _ => _runRowAction(c, action.name),
            },
          ),
        ),
      ),
    );
  }

  Widget _buildCard(FleetContainer c) {
    return _ContainerCard(
      container: c,
      selectionMode: _selectionMode,
      selected: _selectedKeys.contains(_selectionKey(c)),
      onTap: () => _selectionMode ? _toggleSelected(c) : _openDetail(c),
      // Long-press is the usual way into multi-select on touch screens.
      onLongPress: () {
        if (!widget.isAdmin) return;
        if (!_selectionMode) setState(() => _selectionMode = true);
        _toggleSelected(c);
      },
      onRunAction: (action) => _runRowAction(c, action),
      readOnly: !widget.isAdmin,
      ports: _formatPorts(c),
      view: _view(c),
    );
  }
}

/// Distinct values for each container filter, taken from an unfiltered
/// listing. Status also always offers Docker's standard states, so it's
/// usable even before any container is in that state.
class _FilterOptions {
  static const _dockerStates = [
    'created',
    'running',
    'paused',
    'restarting',
    'exited',
    'dead',
  ];

  final Map<String, String> servers;
  final List<String> statuses;
  final List<String> images;
  final Map<String, String> owners;
  final List<String> environments;
  final List<String> tags;

  const _FilterOptions({
    this.servers = const {},
    this.statuses = _dockerStates,
    this.images = const [],
    this.owners = const {},
    this.environments = const [],
    this.tags = const [],
  });

  factory _FilterOptions.from(List<FleetContainer> containers) {
    List<String> sorted(Iterable<String> values) =>
        values.where((v) => v.isNotEmpty).toSet().toList()..sort();

    return _FilterOptions(
      servers: {for (final c in containers) c.serverId: c.serverName},
      statuses: sorted([
        ..._dockerStates,
        ...containers.map((c) => c.container.state),
      ]),
      images: sorted(containers.map((c) => c.container.image).whereType()),
      owners: {
        for (final c in containers)
          if (c.ownerId != null && c.ownerEmail != null)
            c.ownerId!: c.ownerEmail!,
      },
      environments: sorted(containers.map((c) => c.environment).whereType()),
      tags: sorted(containers.expand((c) => c.tags)),
    );
  }
}

/// Phone/narrow layout for one fleet container: name, image and
/// server/state/ports stacked instead of spread across eight table
/// columns, with Start/Stop inline and the rest behind a menu.
class _ContainerCard extends StatelessWidget {
  final FleetContainer container;
  final bool selectionMode;
  final bool selected;
  final VoidCallback onTap;
  final VoidCallback onLongPress;
  final void Function(String action) onRunAction;
  final String ports;
  final ContainerRowView view;
  final bool readOnly;

  const _ContainerCard({
    required this.container,
    required this.selectionMode,
    required this.selected,
    required this.onTap,
    required this.onLongPress,
    required this.onRunAction,
    required this.ports,
    required this.view,
    this.readOnly = false,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final info = container.container;
    final state = info.state;
    final running = state == 'running';
    final paused = state == 'paused';

    return Card(
      margin: const EdgeInsets.symmetric(
        horizontal: Space.lg,
        vertical: Space.xs,
      ),
      color: selected ? theme.colorScheme.primaryContainer : null,
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        onLongPress: readOnly ? null : onLongPress,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(
            Space.lg,
            Space.md,
            Space.xs,
            Space.md,
          ),
          child: Row(
            children: [
              if (selectionMode)
                Padding(
                  padding: const EdgeInsets.only(right: Space.sm),
                  child: Checkbox(value: selected, onChanged: (_) => onTap()),
                ),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Flexible(
                          child: Text(
                            info.name,
                            style: theme.textTheme.titleMedium,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                        const SizedBox(width: Space.sm),
                        StatusPill.of(view.status),
                      ],
                    ),
                    if (view.detail != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 2),
                        child: Text(
                          view.detail!,
                          style: theme.textTheme.bodySmall,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    const SizedBox(height: Space.xs),
                    Text(
                      info.image ?? '—',
                      style: theme.textTheme.bodyMedium,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 2),
                    Text(
                      [
                        container.serverName,
                        if (ports != '—') ports,
                      ].join('  ·  '),
                      style: AppText.mono(context),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                ),
              ),
              if (!selectionMode && !view.reachable)
                const Tooltip(
                  message: 'Server disconnected',
                  child: Padding(
                    padding: EdgeInsets.all(Space.md),
                    child: Icon(Icons.link_off, color: AppColors.textMuted),
                  ),
                ),
              if (!readOnly && !selectionMode && view.reachable) ...[
                running
                    ? IconButton(
                        icon: const Icon(Icons.stop),
                        tooltip: 'Stop',
                        onPressed: () => onRunAction('stop'),
                      )
                    : IconButton(
                        icon: Icon(
                          paused ? Icons.play_circle_outline : Icons.play_arrow,
                        ),
                        tooltip: paused ? 'Resume' : 'Start',
                        onPressed: () =>
                            onRunAction(paused ? 'resume' : 'start'),
                      ),
                PopupMenuButton<String>(
                  tooltip: 'More actions',
                  onSelected: onRunAction,
                  itemBuilder: (context) => [
                    if (running)
                      const PopupMenuItem(
                        value: 'restart',
                        child: Text('Restart'),
                      ),
                    if (running)
                      const PopupMenuItem(value: 'pause', child: Text('Pause')),
                    const PopupMenuDivider(),
                    const PopupMenuItem(
                      value: 'remove',
                      child: Text(
                        'Remove',
                        style: TextStyle(color: AppColors.failed),
                      ),
                    ),
                  ],
                ),
              ],
            ],
          ),
        ),
      ),
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
                    style: FilledButton.styleFrom(
                      backgroundColor: AppColors.failed,
                    ),
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
