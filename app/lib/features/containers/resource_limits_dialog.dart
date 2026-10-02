import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../theme/app_theme.dart';

const _nanoCpusPerCore = 1000000000;
const _bytesPerMebibyte = 1024 * 1024;

/// Shows the resource-limits dialog. Applies live via Docker's
/// ContainerUpdate — no recreate needed, same as the restart-policy dialog.
/// Pops with true on success.
Future<bool?> showResourceLimitsDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required String serverId,
  required String containerId,
  required int currentNanoCpus,
  required int currentMemoryLimitBytes,
  required int currentMemoryReservationBytes,
  required int currentPidsLimit,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) => _ResourceLimitsDialog(
      apiClient: apiClient,
      serverId: serverId,
      containerId: containerId,
      currentNanoCpus: currentNanoCpus,
      currentMemoryLimitBytes: currentMemoryLimitBytes,
      currentMemoryReservationBytes: currentMemoryReservationBytes,
      currentPidsLimit: currentPidsLimit,
    ),
  );
}

class _ResourceLimitsDialog extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final int currentNanoCpus;
  final int currentMemoryLimitBytes;
  final int currentMemoryReservationBytes;
  final int currentPidsLimit;

  const _ResourceLimitsDialog({
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.currentNanoCpus,
    required this.currentMemoryLimitBytes,
    required this.currentMemoryReservationBytes,
    required this.currentPidsLimit,
  });

  @override
  State<_ResourceLimitsDialog> createState() => _ResourceLimitsDialogState();
}

class _ResourceLimitsDialogState extends State<_ResourceLimitsDialog> {
  late final _cpuController = TextEditingController(
    text: widget.currentNanoCpus == 0
        ? ''
        : (widget.currentNanoCpus / _nanoCpusPerCore).toStringAsFixed(2),
  );
  late final _memLimitController = TextEditingController(
    text: widget.currentMemoryLimitBytes == 0
        ? ''
        : '${widget.currentMemoryLimitBytes ~/ _bytesPerMebibyte}',
  );
  late final _memReservationController = TextEditingController(
    text: widget.currentMemoryReservationBytes == 0
        ? ''
        : '${widget.currentMemoryReservationBytes ~/ _bytesPerMebibyte}',
  );
  late final _pidsController = TextEditingController(
    text: widget.currentPidsLimit == 0 ? '' : '${widget.currentPidsLimit}',
  );
  bool _loading = false;
  String? _error;

  @override
  void dispose() {
    _cpuController.dispose();
    _memLimitController.dispose();
    _memReservationController.dispose();
    _pidsController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final cpus = double.tryParse(_cpuController.text.trim()) ?? 0;
    final memLimitMb = int.tryParse(_memLimitController.text.trim()) ?? 0;
    final memReservationMb =
        int.tryParse(_memReservationController.text.trim()) ?? 0;
    final pids = int.tryParse(_pidsController.text.trim()) ?? 0;
    try {
      final result = await widget.apiClient.updateResourceLimits(
        widget.serverId,
        widget.containerId,
        nanoCpus: (cpus * _nanoCpusPerCore).round(),
        memoryLimitBytes: memLimitMb * _bytesPerMebibyte,
        memoryReservationBytes: memReservationMb * _bytesPerMebibyte,
        pidsLimit: pids,
      );
      if (!result.success) {
        setState(() => _error = result.error ?? 'Failed to update limits');
        return;
      }
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() => _error = 'Failed to update limits: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Resource limits'),
      content: SizedBox(
        width: 360,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TextField(
              controller: _cpuController,
              keyboardType: const TextInputType.numberWithOptions(
                decimal: true,
              ),
              decoration: const InputDecoration(
                labelText: 'CPU limit, in cores (blank = unlimited)',
                hintText: 'e.g. 1.5',
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _memLimitController,
              keyboardType: TextInputType.number,
              decoration: const InputDecoration(
                labelText: 'Memory limit, in MB (blank = unlimited)',
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _memReservationController,
              keyboardType: TextInputType.number,
              decoration: const InputDecoration(
                labelText: 'Memory reservation, in MB (blank = none)',
                hintText: 'Soft limit, only enforced under host pressure',
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _pidsController,
              keyboardType: TextInputType.number,
              decoration: const InputDecoration(
                labelText: 'Process limit (blank = unlimited)',
              ),
            ),
            if (_error != null) ...[
              const SizedBox(height: 12),
              Text(_error!, style: const TextStyle(color: AppColors.failed)),
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
