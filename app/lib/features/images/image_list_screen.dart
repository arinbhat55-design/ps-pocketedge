import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';
import '../containers/create_container_dialog.dart';
import 'image_detail_screen.dart';
import 'image_policy_screen.dart';
import 'pull_image_dialog.dart';
import 'registries_screen.dart';
import '../../theme/app_theme.dart';

/// Image management: fleet-wide (or per-server) image inventory with
/// search/filter, pull/remove/prune, dangling detection, and (admin only)
/// tabs for private registry credentials and the approved-image policy.
class ImageListScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const ImageListScreen({
    super.key,
    required this.apiClient,
    required this.isAdmin,
  });

  @override
  State<ImageListScreen> createState() => _ImageListScreenState();
}

class _ImageListScreenState extends State<ImageListScreen> {
  late Future<List<ImageSummary>> _imagesFuture;
  final _searchController = TextEditingController();

  String? _selectedServerId;
  bool _danglingOnly = false;
  bool _pruning = false;
  // Server chip choices. Seeded from the server list and merged with every
  // image load, so picking one server doesn't make the other chips vanish
  // (the image listing itself is filtered server-side). Also lets _openPull
  // resolve the selected server's name without an extra round trip.
  Map<String, String> _serverNames = {};

  @override
  void initState() {
    super.initState();
    _imagesFuture = _load();
    _loadServerNames();
  }

  Future<void> _loadServerNames() async {
    try {
      final servers = await widget.apiClient.listServers();
      if (!mounted) return;
      setState(() {
        _serverNames = {for (final s in servers) s.id: s.name, ..._serverNames};
      });
    } catch (_) {
      // Non-fatal: chips still come from whatever images have loaded.
    }
  }

  bool get _hasActiveFilters =>
      _selectedServerId != null ||
      _danglingOnly ||
      _searchController.text.trim().isNotEmpty;

  void _clearFilters() {
    _searchController.clear();
    final reload = _selectedServerId != null;
    setState(() {
      _selectedServerId = null;
      _danglingOnly = false;
    });
    if (reload) _refresh();
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<List<ImageSummary>> _load() {
    return widget.apiClient.listImages(serverId: _selectedServerId);
  }

  void _refresh() {
    setState(() {
      _imagesFuture = _load();
    });
  }

  List<ImageSummary> _filter(List<ImageSummary> images) {
    final query = _searchController.text.trim().toLowerCase();
    return images.where((img) {
      if (_danglingOnly && !img.dangling) return false;
      if (query.isEmpty) return true;
      return img.repoTags.any((t) => t.toLowerCase().contains(query)) ||
          img.id.toLowerCase().contains(query);
    }).toList();
  }

  Future<void> _openPull() async {
    final serverName = _selectedServerId == null
        ? null
        : _serverNames[_selectedServerId];
    final pulled = await showPullImageDialog(
      context,
      apiClient: widget.apiClient,
      serverId: _selectedServerId,
      serverName: serverName,
    );
    if (pulled == true) _refresh();
  }

  Future<void> _openDetail(ImageSummary image) async {
    await Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) =>
            ImageDetailScreen(apiClient: widget.apiClient, image: image),
      ),
    );
    _refresh();
  }

  /// Opens the create-container sheet pre-filled with this image, pinned
  /// to the server it lives on (an image only exists on the server it was
  /// pulled to, so there's nothing to pick).
  Future<void> _runFromImage(ImageSummary image) async {
    final newId = await showCreateContainerDialog(
      context,
      apiClient: widget.apiClient,
      serverId: image.serverId,
      serverName: image.serverName,
      initialImage: image.repoTags.isNotEmpty ? image.repoTags.first : image.id,
    );
    if (newId != null) _refresh();
  }

  Future<void> _removeImage(ImageSummary image) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Remove image?'),
        content: Text(
          'This permanently deletes ${image.repoTags.isNotEmpty ? image.repoTags.join(', ') : image.shortId} '
          'from ${image.serverName}.'
          '${image.containersCount > 0 ? '\n\nIt is currently used by ${image.containersCount} container(s) — removal will fail unless forced.' : ''}',
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
      final result = await widget.apiClient.removeImage(
        image.serverId,
        image.id,
        force: image.containersCount > 0,
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
        ).showSnackBar(SnackBar(content: Text('Failed to remove image: $e')));
      }
    }
  }

  Future<void> _prune({required bool all}) async {
    final serverId = _selectedServerId;
    if (serverId == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Select a single server to prune its images'),
        ),
      );
      return;
    }

    setState(() => _pruning = true);
    try {
      final result = await widget.apiClient.pruneImages(serverId, all: all);
      if (!mounted) return;
      if (!result.success) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(result.error ?? 'Prune failed')));
        return;
      }
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('Reclaimed ${formatBytes(result.reclaimedBytes)}'),
        ),
      );
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Prune failed: $e')));
      }
    } finally {
      if (mounted) setState(() => _pruning = false);
    }
  }

  PrimaryAction get _pullAction => PrimaryAction(
    label: 'Pull image',
    icon: Icons.download,
    onPressed: _openPull,
  );

  Widget _buildImagesTab() {
    return Scaffold(
      floatingActionButton: widget.isAdmin ? _pullAction.fab(context) : null,
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          PageIntro(
            description:
                'Images stored on your servers. Run a container from one, '
                'or prune what nothing uses.',
            // Admins see this as a tab under a shared AppBar, so the
            // action lives here rather than in the AppBar.
            action: widget.isAdmin ? _pullAction.inline(context) : null,
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(Space.lg, Space.md, Space.lg, 0),
            child: TextField(
              controller: _searchController,
              onChanged: (_) => setState(() {}),
              decoration: InputDecoration(
                hintText: 'Search by tag or id',
                prefixIcon: const Icon(Icons.search),
                suffixIcon: _searchController.text.isEmpty
                    ? null
                    : IconButton(
                        icon: const Icon(Icons.clear),
                        tooltip: 'Clear search',
                        onPressed: () =>
                            setState(() => _searchController.clear()),
                      ),
                isDense: true,
                border: const OutlineInputBorder(),
              ),
            ),
          ),
          Expanded(
            child: FutureBuilder<List<ImageSummary>>(
              future: _imagesFuture,
              builder: (context, snapshot) {
                if (snapshot.connectionState == ConnectionState.waiting) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError) {
                  return StateMessage.error(
                    what: 'images',
                    error: snapshot.error,
                    onRetry: _refresh,
                  );
                }
                final all = snapshot.data ?? [];
                _serverNames = {
                  ..._serverNames,
                  for (final img in all) img.serverId: img.serverName,
                };
                final servers = _serverNames;
                final images = _filter(all);

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
                            label: const Text('Dangling only'),
                            selected: _danglingOnly,
                            onSelected: (v) =>
                                setState(() => _danglingOnly = v),
                          ),
                          if (_hasActiveFilters)
                            TextButton.icon(
                              onPressed: _clearFilters,
                              icon: const Icon(Icons.filter_alt_off, size: 18),
                              label: const Text('Clear filters'),
                            ),
                          if (widget.isAdmin && _selectedServerId != null) ...[
                            const SizedBox(width: 4),
                            const VerticalDivider(width: 1),
                            const SizedBox(width: 4),
                            OutlinedButton.icon(
                              onPressed: _pruning
                                  ? null
                                  : () => _prune(all: false),
                              icon: const Icon(
                                Icons.cleaning_services,
                                size: 16,
                              ),
                              label: const Text('Prune dangling'),
                            ),
                            OutlinedButton.icon(
                              onPressed: _pruning
                                  ? null
                                  : () => _prune(all: true),
                              icon: const Icon(Icons.delete_sweep, size: 16),
                              label: const Text('Prune all unused'),
                            ),
                          ],
                        ],
                      ),
                    ),
                    Expanded(
                      child: images.isEmpty
                          ? _buildEmptyState()
                          : LayoutBuilder(
                              builder: (context, constraints) =>
                                  constraints.maxWidth < kTableMinWidth
                                  ? ListView.builder(
                                      // Room for the FAB under the last card.
                                      padding: const EdgeInsets.only(
                                        top: 4,
                                        bottom: 88,
                                      ),
                                      itemCount: images.length,
                                      itemBuilder: (_, i) => _ImageCard(
                                        image: images[i],
                                        onOpenDetail: _openDetail,
                                        onRun: _runFromImage,
                                        onRemove: _removeImage,
                                      ),
                                    )
                                  : _ImageTable(
                                      images: images,
                                      onOpenDetail: _openDetail,
                                      onRun: _runFromImage,
                                      onRemove: _removeImage,
                                    ),
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

  Widget _buildEmptyState() {
    if (_hasActiveFilters) {
      return StateMessage(
        icon: Icons.filter_alt_off_outlined,
        title: 'No images match',
        message:
            'Nothing matches the current search, server, or dangling '
            'filter. Clear them to see every image.',
        actionLabel: 'Clear filters',
        actionIcon: Icons.filter_alt_off,
        onAction: _clearFilters,
      );
    }
    return StateMessage(
      icon: Icons.inventory_2_outlined,
      title: 'No images yet',
      message:
          'Images pulled or built on your servers show up here. Pull one '
          'from a registry to get started.',
      actionLabel: 'Pull image',
      actionIcon: Icons.download,
      onAction: _openPull,
    );
  }

  @override
  Widget build(BuildContext context) {
    if (!widget.isAdmin) {
      return Scaffold(
        appBar: AppBar(
          title: const Text('Images'),
          actions: [?_pullAction.appBarAction(context)],
        ),
        body: _buildImagesTab(),
      );
    }

    return DefaultTabController(
      length: 3,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('Images'),
          bottom: const TabBar(
            tabs: [
              Tab(text: 'Images'),
              Tab(text: 'Registries'),
              Tab(text: 'Policy'),
            ],
          ),
        ),
        body: TabBarView(
          children: [
            _buildImagesTab(),
            RegistriesScreen(apiClient: widget.apiClient),
            ImagePolicyScreen(apiClient: widget.apiClient),
          ],
        ),
      ),
    );
  }
}

/// The fleet-image table: an [Axis.horizontal]-scrolling [DataTable] with
/// its own [ScrollController] (needed for [Scrollbar.thumbVisibility] to
/// actually attach — without one the thumb paints but dragging it does
/// nothing) and mouse opted into drag-to-scroll, since the platform
/// default only allows touch/stylus/trackpad there.
class _ImageTable extends StatefulWidget {
  final List<ImageSummary> images;
  final void Function(ImageSummary) onOpenDetail;
  final void Function(ImageSummary) onRun;
  final void Function(ImageSummary) onRemove;

  const _ImageTable({
    required this.images,
    required this.onOpenDetail,
    required this.onRun,
    required this.onRemove,
  });

  @override
  State<_ImageTable> createState() => _ImageTableState();
}

class _ImageTableState extends State<_ImageTable> {
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
      // Horizontal is the outer scroll (bounded to the viewport height by
      // the ancestor Expanded), so its Scrollbar thumb stays pinned at the
      // bottom of what's visible. Nesting it the other way around — as
      // originally written — put the horizontal scrollbar at the bottom of
      // the full (unscrolled) row list instead, unreachable without first
      // scrolling all the way down.
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
                  DataColumn(label: Text('REPOSITORY:TAG')),
                  DataColumn(label: Text('STATUS')),
                  DataColumn(label: Text('SERVER')),
                  DataColumn(label: Text('SIZE'), numeric: true),
                  DataColumn(label: Text('CONTAINERS'), numeric: true),
                  DataColumn(label: Text('CREATED')),
                  DataColumn(label: Text('ID')),
                  DataColumn(label: Text('')),
                ],
                rows: widget.images.map(_buildRow).toList(),
              ),
            ),
          ),
        ),
      ),
    );
  }

  DataRow _buildRow(ImageSummary img) {
    final title = _imageTitle(img);

    return DataRow(
      onSelectChanged: (_) => widget.onOpenDetail(img),
      cells: [
        DataCell(
          Tooltip(
            message: title,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 280),
              child: Text(
                title,
                overflow: TextOverflow.ellipsis,
                maxLines: 1,
                style: Theme.of(context).textTheme.titleSmall,
              ),
            ),
          ),
        ),
        DataCell(StatusPill.of(_imageStatus(img))),
        DataCell(Text(img.serverName)),
        DataCell(Text(formatBytes(img.sizeBytes))),
        DataCell(Text('${img.containersCount}')),
        DataCell(
          Text(
            img.createdAt.toLocal().toString().split('.').first,
            style: AppText.mono(context),
          ),
        ),
        DataCell(Text(img.shortId, style: AppText.mono(context))),
        DataCell(
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              IconButton(
                icon: const Icon(Icons.play_arrow, size: 18),
                tooltip: 'Run container from this image',
                visualDensity: VisualDensity.compact,
                onPressed: () => widget.onRun(img),
              ),
              IconButton(
                icon: const Icon(Icons.delete_outline, size: 18),
                tooltip: 'Remove',
                visualDensity: VisualDensity.compact,
                onPressed: () => widget.onRemove(img),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

/// Phone/narrow layout for one image: tag, id and server/size/usage
/// stacked instead of spread across eight table columns.
class _ImageCard extends StatelessWidget {
  final ImageSummary image;
  final void Function(ImageSummary) onOpenDetail;
  final void Function(ImageSummary) onRun;
  final void Function(ImageSummary) onRemove;

  const _ImageCard({
    required this.image,
    required this.onOpenDetail,
    required this.onRun,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final title = _imageTitle(image);

    return Card(
      margin: const EdgeInsets.symmetric(
        horizontal: Space.lg,
        vertical: Space.xs,
      ),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: () => onOpenDetail(image),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(
            Space.lg,
            Space.md,
            Space.xs,
            Space.md,
          ),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Flexible(
                          child: Text(
                            title,
                            style: theme.textTheme.titleMedium,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                        const SizedBox(width: Space.sm),
                        StatusPill.of(_imageStatus(image)),
                      ],
                    ),
                    const SizedBox(height: Space.xs),
                    Text(
                      [
                        image.serverName,
                        formatBytes(image.sizeBytes),
                        image.containersCount == 1
                            ? '1 container'
                            : '${image.containersCount} containers',
                      ].join('  ·  '),
                      style: theme.textTheme.bodyMedium,
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 2),
                    Text(image.shortId, style: AppText.mono(context)),
                  ],
                ),
              ),
              IconButton(
                icon: const Icon(Icons.play_arrow),
                tooltip: 'Run container from this image',
                onPressed: () => onRun(image),
              ),
              IconButton(
                icon: const Icon(Icons.delete_outline),
                tooltip: 'Remove',
                onPressed: () => onRemove(image),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

String _imageTitle(ImageSummary img) =>
    img.repoTags.isNotEmpty ? img.repoTags.join(', ') : 'Untagged image';

StatusLabel _imageStatus(ImageSummary img) => img.dangling
    ? (label: 'Dangling', tone: StatusTone.warning)
    : img.containersCount > 0
    ? (label: 'In use', tone: StatusTone.healthy)
    : (label: 'Unused', tone: StatusTone.neutral);
