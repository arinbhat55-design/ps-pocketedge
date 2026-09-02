import 'dart:async';

import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart';
import '../../models/server.dart';

/// Shows the pull-image sheet: pick a server (unless preset), search a
/// registry (Docker Hub by default, or a configured private registry) to
/// find an image, pick a tag, then pull. Pops with `true` on a successful
/// pull, or null if cancelled.
Future<bool?> showPullImageDialog(
  BuildContext context, {
  required ApiClient apiClient,
  String? serverId,
  String? serverName,
}) {
  return showModalBottomSheet<bool>(
    context: context,
    isScrollControlled: true,
    builder: (_) => _PullImageSheet(
      apiClient: apiClient,
      presetServerId: serverId,
      presetServerName: serverName,
    ),
  );
}

class _PullImageSheet extends StatefulWidget {
  final ApiClient apiClient;
  final String? presetServerId;
  final String? presetServerName;

  const _PullImageSheet({
    required this.apiClient,
    this.presetServerId,
    this.presetServerName,
  });

  @override
  State<_PullImageSheet> createState() => _PullImageSheetState();
}

class _PullImageSheetState extends State<_PullImageSheet> {
  late final Future<List<Server>>? _serversFuture =
      widget.presetServerId == null ? widget.apiClient.listServers() : null;
  late final Future<List<Registry>> _registriesFuture =
      widget.apiClient.listRegistries();

  String? _selectedServerId;
  String? _selectedRegistryId;
  final _imageController = TextEditingController();
  final _tagController = TextEditingController(text: 'latest');
  final _searchController = TextEditingController();
  Timer? _debounce;

  List<RegistrySearchResult> _searchResults = [];
  List<String> _tags = [];
  bool _searching = false;
  bool _loadingTags = false;
  bool _pulling = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _selectedServerId = widget.presetServerId;
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _imageController.dispose();
    _tagController.dispose();
    _searchController.dispose();
    super.dispose();
  }

  void _onSearchChanged(String query) {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 400), () async {
      if (query.trim().isEmpty) {
        setState(() => _searchResults = []);
        return;
      }
      setState(() => _searching = true);
      try {
        final results = await widget.apiClient.searchRegistries(
          query: query.trim(),
          registryId: _selectedRegistryId,
        );
        if (mounted) setState(() => _searchResults = results);
      } catch (_) {
        // Search failures aren't fatal — the user can still type an image
        // reference directly.
      } finally {
        if (mounted) setState(() => _searching = false);
      }
    });
  }

  Future<void> _selectImage(String image) async {
    setState(() {
      _imageController.text = image;
      _searchResults = [];
      _tags = [];
      _loadingTags = true;
    });
    try {
      final tags = await widget.apiClient.listImageTags(
        image,
        registryId: _selectedRegistryId,
      );
      if (mounted) setState(() => _tags = tags);
    } catch (_) {
      // No tags found (private/unlisted repo, etc.) — the user can still
      // type a tag manually.
    } finally {
      if (mounted) setState(() => _loadingTags = false);
    }
  }

  Future<void> _submit() async {
    final serverId = _selectedServerId;
    final image = _imageController.text.trim();
    final tag = _tagController.text.trim();
    if (serverId == null || image.isEmpty) {
      setState(() => _error = 'Server and image are required');
      return;
    }

    setState(() {
      _pulling = true;
      _error = null;
    });
    try {
      final ref = tag.isEmpty ? image : '$image:$tag';
      final result = await widget.apiClient.pullImage(
        serverId,
        ref,
        registryId: _selectedRegistryId,
      );
      if (!mounted) return;
      if (!result.success) {
        setState(() => _error = result.error ?? 'Pull failed');
        return;
      }
      Navigator.of(context).pop(true);
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _pulling = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(
        left: 16,
        right: 16,
        top: 16,
        bottom: MediaQuery.of(context).viewInsets.bottom + 16,
      ),
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Pull image', style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 16),
            if (widget.presetServerId != null)
              Text('Server: ${widget.presetServerName}')
            else
              FutureBuilder<List<Server>>(
                future: _serversFuture,
                builder: (context, snapshot) {
                  final servers = snapshot.data ?? [];
                  return DropdownButtonFormField<String>(
                    initialValue: _selectedServerId,
                    decoration: const InputDecoration(labelText: 'Server'),
                    items: [
                      for (final s in servers)
                        DropdownMenuItem(value: s.id, child: Text(s.name)),
                    ],
                    onChanged: (value) =>
                        setState(() => _selectedServerId = value),
                  );
                },
              ),
            const SizedBox(height: 12),
            FutureBuilder<List<Registry>>(
              future: _registriesFuture,
              builder: (context, snapshot) {
                final registries = snapshot.data ?? [];
                if (registries.isEmpty) return const SizedBox.shrink();
                return DropdownButtonFormField<String?>(
                  initialValue: _selectedRegistryId,
                  decoration: const InputDecoration(
                    labelText: 'Registry (blank = Docker Hub)',
                  ),
                  items: [
                    const DropdownMenuItem(
                      value: null,
                      child: Text('Docker Hub (public)'),
                    ),
                    for (final r in registries)
                      DropdownMenuItem(value: r.id, child: Text(r.name)),
                  ],
                  onChanged: (value) =>
                      setState(() => _selectedRegistryId = value),
                );
              },
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _searchController,
              onChanged: _onSearchChanged,
              decoration: InputDecoration(
                labelText: 'Search registry',
                prefixIcon: const Icon(Icons.search),
                suffixIcon: _searching
                    ? const Padding(
                        padding: EdgeInsets.all(12),
                        child: SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        ),
                      )
                    : null,
                border: const OutlineInputBorder(),
              ),
            ),
            if (_searchResults.isNotEmpty)
              Container(
                constraints: const BoxConstraints(maxHeight: 200),
                margin: const EdgeInsets.only(top: 4),
                decoration: BoxDecoration(
                  border: Border.all(color: Theme.of(context).dividerColor),
                ),
                child: ListView(
                  shrinkWrap: true,
                  children: [
                    for (final r in _searchResults)
                      ListTile(
                        dense: true,
                        title: Text(r.name),
                        subtitle: r.description.isEmpty
                            ? null
                            : Text(
                                r.description,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                              ),
                        trailing: r.official
                            ? const Icon(Icons.verified, size: 16)
                            : null,
                        onTap: () => _selectImage(r.name),
                      ),
                  ],
                ),
              ),
            const SizedBox(height: 12),
            TextField(
              controller: _imageController,
              decoration: const InputDecoration(
                labelText: 'Image',
                hintText: 'e.g. nginx or myregistry.com/team/app',
                border: OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 12),
            if (_loadingTags)
              const LinearProgressIndicator()
            else if (_tags.isNotEmpty)
              Wrap(
                spacing: 8,
                runSpacing: 4,
                children: [
                  for (final tag in _tags.take(30))
                    ChoiceChip(
                      label: Text(tag),
                      selected: _tagController.text == tag,
                      onSelected: (_) =>
                          setState(() => _tagController.text = tag),
                    ),
                ],
              ),
            const SizedBox(height: 8),
            TextField(
              controller: _tagController,
              decoration: const InputDecoration(
                labelText: 'Tag',
                border: OutlineInputBorder(),
              ),
            ),
            if (_error != null) ...[
              const SizedBox(height: 12),
              Text(_error!, style: const TextStyle(color: Colors.red)),
            ],
            const SizedBox(height: 16),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                TextButton(
                  onPressed: _pulling
                      ? null
                      : () => Navigator.of(context).pop(),
                  child: const Text('Cancel'),
                ),
                const SizedBox(width: 8),
                FilledButton.icon(
                  onPressed: _pulling ? null : _submit,
                  icon: _pulling
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.download),
                  label: Text(_pulling ? 'Pulling…' : 'Pull'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
