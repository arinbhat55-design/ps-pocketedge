import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart';
import 'image_detail_screen.dart';
import 'image_policy_screen.dart';
import 'pull_image_dialog.dart';
import 'registries_screen.dart';

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
  // Populated on every build so _openPull can resolve the selected
  // server's name without an extra round trip.
  Map<String, String> _serverNames = {};

  @override
  void initState() {
    super.initState();
    _imagesFuture = _load();
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
    setState(() => _imagesFuture = _load());
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
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
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
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(result.error ?? 'Remove failed')));
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

  Widget _buildImagesTab() {
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _openPull,
        icon: const Icon(Icons.download),
        label: const Text('Pull image'),
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 0),
            child: TextField(
              controller: _searchController,
              onChanged: (_) => setState(() {}),
              decoration: const InputDecoration(
                hintText: 'Search by tag or id',
                prefixIcon: Icon(Icons.search),
                isDense: true,
                border: OutlineInputBorder(),
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
                  return Center(
                    child: Text('Failed to load images: ${snapshot.error}'),
                  );
                }
                final all = snapshot.data ?? [];
                final servers = <String, String>{
                  for (final img in all) img.serverId: img.serverName,
                };
                _serverNames = servers;
                final images = _filter(all);

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
                            label: const Text('Dangling only'),
                            selected: _danglingOnly,
                            onSelected: (v) =>
                                setState(() => _danglingOnly = v),
                          ),
                          if (_selectedServerId != null) ...[
                            const SizedBox(width: 4),
                            const VerticalDivider(width: 1),
                            const SizedBox(width: 4),
                            OutlinedButton.icon(
                              onPressed: _pruning
                                  ? null
                                  : () => _prune(all: false),
                              icon: const Icon(Icons.cleaning_services, size: 16),
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
                          ? const Center(child: Text('No images found.'))
                          : ListView(
                              padding: const EdgeInsets.symmetric(vertical: 4),
                              children: images.map(_buildTile).toList(),
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

  Widget _buildTile(ImageSummary img) {
    final title = img.repoTags.isNotEmpty
        ? img.repoTags.join(', ')
        : '${img.shortId} (dangling)';
    return ListTile(
      dense: true,
      leading: Icon(
        img.dangling ? Icons.help_outline : Icons.inventory_2_outlined,
        color: img.dangling ? Colors.orange : null,
      ),
      title: Text(title),
      subtitle: Text(
        '${img.serverName} • ${formatBytes(img.sizeBytes)} • '
        '${img.createdAt.toLocal().toString().split('.').first}',
      ),
      trailing: IconButton(
        icon: const Icon(Icons.delete_outline),
        tooltip: 'Remove',
        onPressed: () => _removeImage(img),
      ),
      onTap: () => _openDetail(img),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (!widget.isAdmin) return _buildImagesTab();

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
