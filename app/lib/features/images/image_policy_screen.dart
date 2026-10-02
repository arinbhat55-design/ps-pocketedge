import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/image.dart';

/// Admin-only: toggles enforcement of the approved-image allowlist (used
/// at container create/recreate and stack deploy/redeploy time) and
/// manages the allowlist's patterns.
class ImagePolicyScreen extends StatefulWidget {
  final ApiClient apiClient;

  const ImagePolicyScreen({super.key, required this.apiClient});

  @override
  State<ImagePolicyScreen> createState() => _ImagePolicyScreenState();
}

class _ImagePolicyScreenState extends State<ImagePolicyScreen> {
  late Future<bool> _enabledFuture;
  late Future<List<ApprovedImage>> _patternsFuture;
  bool _togglingPolicy = false;

  @override
  void initState() {
    super.initState();
    _enabledFuture = widget.apiClient.getImagePolicyEnabled();
    _patternsFuture = widget.apiClient.listApprovedImages();
  }

  void _refreshPatterns() {
    setState(() {
      _patternsFuture = widget.apiClient.listApprovedImages();
    });
  }

  Future<void> _toggle(bool value) async {
    setState(() => _togglingPolicy = true);
    try {
      await widget.apiClient.setImagePolicyEnabled(value);
      if (mounted) {
        setState(() {
          _enabledFuture = Future.value(value);
        });
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to update policy: $e')));
      }
    } finally {
      if (mounted) setState(() => _togglingPolicy = false);
    }
  }

  Future<void> _addPattern() async {
    final patternController = TextEditingController();
    final noteController = TextEditingController();

    final saved = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Add approved image pattern'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              controller: patternController,
              decoration: const InputDecoration(
                labelText: 'Pattern',
                hintText: 'nginx, nginx:1.27, nginx:1.*, ghcr.io/acme/*',
                helperText:
                    'A repository allows all its tags; * matches anything.',
              ),
            ),
            TextField(
              controller: noteController,
              decoration: const InputDecoration(labelText: 'Note (optional)'),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Add'),
          ),
        ],
      ),
    );

    if (saved != true || patternController.text.trim().isEmpty) return;

    try {
      await widget.apiClient.createApprovedImage(
        patternController.text.trim(),
        note: noteController.text.trim(),
      );
      _refreshPatterns();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to add pattern: $e')));
      }
    }
  }

  Future<void> _deletePattern(ApprovedImage pattern) async {
    try {
      await widget.apiClient.deleteApprovedImage(pattern.id);
      _refreshPatterns();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to remove pattern: $e')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _addPattern,
        icon: const Icon(Icons.add),
        label: const Text('Add pattern'),
      ),
      body: ListView(
        children: [
          FutureBuilder<bool>(
            future: _enabledFuture,
            builder: (context, snapshot) {
              final enabled = snapshot.data ?? false;
              return SwitchListTile(
                title: const Text('Restrict deployment to approved images'),
                subtitle: const Text(
                  'When on, container create/recreate and stack deploy/redeploy '
                  'are rejected unless the image matches a pattern below.',
                ),
                value: enabled,
                onChanged: _togglingPolicy || !snapshot.hasData
                    ? null
                    : _toggle,
              );
            },
          ),
          const Divider(),
          FutureBuilder<List<ApprovedImage>>(
            future: _patternsFuture,
            builder: (context, snapshot) {
              if (snapshot.connectionState == ConnectionState.waiting) {
                return const Padding(
                  padding: EdgeInsets.all(24),
                  child: Center(child: CircularProgressIndicator()),
                );
              }
              if (snapshot.hasError) {
                return Padding(
                  padding: const EdgeInsets.all(24),
                  child: Text('Failed to load patterns: ${snapshot.error}'),
                );
              }
              final patterns = snapshot.data ?? [];
              if (patterns.isEmpty) {
                return const Padding(
                  padding: EdgeInsets.all(24),
                  child: Text('No approved image patterns configured yet.'),
                );
              }
              return Column(
                children: [
                  for (final p in patterns)
                    ListTile(
                      leading: const Icon(Icons.check_circle_outline),
                      title: Text(p.pattern),
                      subtitle: p.note.isEmpty ? null : Text(p.note),
                      trailing: IconButton(
                        icon: const Icon(Icons.delete_outline),
                        onPressed: () => _deletePattern(p),
                      ),
                    ),
                ],
              );
            },
          ),
        ],
      ),
    );
  }
}
