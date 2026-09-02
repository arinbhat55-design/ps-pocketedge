import 'package:flutter/material.dart';

import '../../api/api_client.dart';

/// Shows the rename dialog. Pops with true on success, false/null if
/// cancelled or failed.
Future<bool?> showRenameContainerDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required String serverId,
  required String containerId,
  required String currentName,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) => _RenameContainerDialog(
      apiClient: apiClient,
      serverId: serverId,
      containerId: containerId,
      currentName: currentName,
    ),
  );
}

class _RenameContainerDialog extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String currentName;

  const _RenameContainerDialog({
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.currentName,
  });

  @override
  State<_RenameContainerDialog> createState() => _RenameContainerDialogState();
}

class _RenameContainerDialogState extends State<_RenameContainerDialog> {
  late final _nameController = TextEditingController(text: widget.currentName);
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
      final result = await widget.apiClient.renameContainer(
        widget.serverId,
        widget.containerId,
        newName,
      );
      if (!result.success) {
        setState(() => _error = result.error ?? 'Failed to rename container');
        return;
      }
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() => _error = 'Failed to rename container: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Rename container'),
      content: SizedBox(
        width: 360,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TextField(
              controller: _nameController,
              decoration: const InputDecoration(labelText: 'New name'),
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
          onPressed: _loading ? null : () => Navigator.of(context).pop(false),
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
              : const Text('Rename'),
        ),
      ],
    );
  }
}
