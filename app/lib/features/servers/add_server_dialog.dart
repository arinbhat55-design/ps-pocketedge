import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../theme/app_theme.dart';

class AddServerDialog extends StatefulWidget {
  final ApiClient apiClient;

  const AddServerDialog({super.key, required this.apiClient});

  @override
  State<AddServerDialog> createState() => _AddServerDialogState();
}

class _AddServerDialogState extends State<AddServerDialog> {
  EnrollmentToken? _token;
  String? _error;

  @override
  void initState() {
    super.initState();
    _generate();
  }

  Future<void> _generate() async {
    setState(() {
      _token = null;
      _error = null;
    });
    try {
      final token = await widget.apiClient.createEnrollmentToken();
      if (mounted) setState(() => _token = token);
    } catch (e) {
      if (mounted) setState(() => _error = 'Failed to generate token: $e');
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Add server'),
      content: SizedBox(
        width: 480,
        child: _error != null
            ? Text(_error!, style: const TextStyle(color: AppColors.failed))
            : _token == null
            ? const Center(
                child: Padding(
                  padding: EdgeInsets.all(24),
                  child: CircularProgressIndicator(),
                ),
              )
            : Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text(
                    'Run this on the target machine (Linux server, mini-PC, or Raspberry Pi). '
                    'The token is single-use.',
                  ),
                  const SizedBox(height: 12),
                  _CopyableCommand(command: _token!.installHint),
                  const SizedBox(height: 8),
                  Text(
                    'Token expires: ${_token!.expiresAt.toLocal()}',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
              ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
      ],
    );
  }
}

class _CopyableCommand extends StatelessWidget {
  final String command;

  const _CopyableCommand({required this.command});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Expanded(
            child: SelectableText(
              command,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
            ),
          ),
          IconButton(
            icon: const Icon(Icons.copy, size: 18),
            tooltip: 'Copy',
            onPressed: () {
              Clipboard.setData(ClipboardData(text: command));
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(content: Text('Copied install command')),
              );
            },
          ),
        ],
      ),
    );
  }
}
