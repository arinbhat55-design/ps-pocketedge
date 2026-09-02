import 'package:flutter/material.dart';

import '../../api/api_client.dart';

const _restartPolicyLabels = {
  'no': 'No',
  'on-failure': 'On failure',
  'always': 'Always',
  'unless-stopped': 'Unless stopped',
};

/// Shows the restart-policy dialog. Applies live via Docker's
/// ContainerUpdate — no recreate needed. Pops with true on success.
Future<bool?> showRestartPolicyDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required String serverId,
  required String containerId,
  required String currentPolicyName,
  required int currentMaxRetryCount,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) => _RestartPolicyDialog(
      apiClient: apiClient,
      serverId: serverId,
      containerId: containerId,
      currentPolicyName: currentPolicyName,
      currentMaxRetryCount: currentMaxRetryCount,
    ),
  );
}

class _RestartPolicyDialog extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String currentPolicyName;
  final int currentMaxRetryCount;

  const _RestartPolicyDialog({
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.currentPolicyName,
    required this.currentMaxRetryCount,
  });

  @override
  State<_RestartPolicyDialog> createState() => _RestartPolicyDialogState();
}

class _RestartPolicyDialogState extends State<_RestartPolicyDialog> {
  late String _policy =
      _restartPolicyLabels.containsKey(widget.currentPolicyName)
      ? widget.currentPolicyName
      : 'no';
  late final _maxRetryController = TextEditingController(
    text: widget.currentMaxRetryCount == 0
        ? ''
        : '${widget.currentMaxRetryCount}',
  );
  bool _loading = false;
  String? _error;

  @override
  void dispose() {
    _maxRetryController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final maxRetry =
        _policy == 'on-failure' && _maxRetryController.text.trim().isNotEmpty
        ? int.tryParse(_maxRetryController.text.trim()) ?? 0
        : 0;
    try {
      final result = await widget.apiClient.updateRestartPolicy(
        widget.serverId,
        widget.containerId,
        _policy,
        maxRetry,
      );
      if (!result.success) {
        setState(
          () => _error = result.error ?? 'Failed to update restart policy',
        );
        return;
      }
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() => _error = 'Failed to update restart policy: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Restart policy'),
      content: SizedBox(
        width: 360,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            DropdownButtonFormField<String>(
              initialValue: _policy,
              items: [
                for (final entry in _restartPolicyLabels.entries)
                  DropdownMenuItem(value: entry.key, child: Text(entry.value)),
              ],
              onChanged: (v) => setState(() => _policy = v ?? 'no'),
            ),
            if (_policy == 'on-failure') ...[
              const SizedBox(height: 12),
              TextField(
                controller: _maxRetryController,
                keyboardType: TextInputType.number,
                decoration: const InputDecoration(
                  labelText: 'Max retry count (0 = unlimited)',
                ),
              ),
            ],
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
              : const Text('Save'),
        ),
      ],
    );
  }
}
