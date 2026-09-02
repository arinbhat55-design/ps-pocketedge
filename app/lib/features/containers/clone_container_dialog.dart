import 'package:flutter/material.dart';

import '../../api/api_client.dart';

/// Shows the clone dialog. Pops with the new container's id on success,
/// null if cancelled or failed.
Future<String?> showCloneContainerDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required String serverId,
  required String containerId,
  required String currentName,
}) {
  return showDialog<String>(
    context: context,
    builder: (_) => _CloneContainerDialog(
      apiClient: apiClient,
      serverId: serverId,
      containerId: containerId,
      currentName: currentName,
    ),
  );
}

class _CloneContainerDialog extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String currentName;

  const _CloneContainerDialog({
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.currentName,
  });

  @override
  State<_CloneContainerDialog> createState() => _CloneContainerDialogState();
}

class _CloneContainerDialogState extends State<_CloneContainerDialog> {
  late final _nameController = TextEditingController(
    text: '${widget.currentName}-clone',
  );
  bool _loading = false;
  String? _error;

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final newName = _nameController.text.trim();
    if (newName.isEmpty) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await widget.apiClient.cloneContainer(
        widget.serverId,
        widget.containerId,
        newName,
      );
      if (!result.success) {
        setState(() => _error = result.error ?? 'Failed to clone container');
        return;
      }
      if (mounted) Navigator.of(context).pop(result.containerId);
    } catch (e) {
      setState(() => _error = 'Failed to clone container: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Clone container'),
      content: SizedBox(
        width: 360,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Text(
              "Duplicates this container's configuration into a new, "
              'not-started container. Data in volumes is not copied.',
              style: TextStyle(fontSize: 12),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _nameController,
              decoration: const InputDecoration(
                labelText: 'New container name',
              ),
              autofocus: true,
              onSubmitted: (_) => _submit(),
            ),
            if (_error != null) ...[
              const SizedBox(height: 12),
              Text(_error!, style: const TextStyle(color: Colors.red)),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: _loading ? null : () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _loading ? null : _submit,
          child: _loading
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Clone'),
        ),
      ],
    );
  }
}
