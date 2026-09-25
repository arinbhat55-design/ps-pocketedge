import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/volume.dart';
import '../../theme/app_theme.dart';

/// Shows the volume-delete confirmation dialog: the user must type the
/// volume's exact name to enable the delete button (the client-side half of
/// the delete-protection guard; the server-side half is the in-use check in
/// internal/agent/docker/volumes.go's RemoveVolume). If the first attempt
/// comes back blocked because the volume is still in use, a second "force
/// delete" step is offered rather than silently retrying. Pops with `true`
/// on a successful delete.
Future<bool?> showVolumeDeleteDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required VolumeSummary volume,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) => _VolumeDeleteDialog(apiClient: apiClient, volume: volume),
  );
}

class _VolumeDeleteDialog extends StatefulWidget {
  final ApiClient apiClient;
  final VolumeSummary volume;

  const _VolumeDeleteDialog({required this.apiClient, required this.volume});

  @override
  State<_VolumeDeleteDialog> createState() => _VolumeDeleteDialogState();
}

class _VolumeDeleteDialogState extends State<_VolumeDeleteDialog> {
  final _confirmController = TextEditingController();
  bool _loading = false;
  String? _error;
  // Set once a non-forced delete comes back blocked by the in-use guard —
  // switches the primary action to a force-delete retry rather than
  // resubmitting the same request.
  bool _offerForce = false;

  @override
  void dispose() {
    _confirmController.dispose();
    super.dispose();
  }

  bool get _nameMatches => _confirmController.text.trim() == widget.volume.name;

  Future<void> _submit({required bool force}) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await widget.apiClient.removeVolume(
        widget.volume.serverId,
        widget.volume.name,
        force: force,
      );
      if (!mounted) return;
      if (!result.success) {
        // Only offer a force-delete retry when the failure actually looks
        // like the in-use guard (internal/agent/docker/volumes.go's
        // RemoveVolume) — surfacing "force delete?" for an unrelated
        // failure (a disconnected agent's docker-level error, a permission
        // problem, etc.) would mislabel the cause and offer a retry that
        // can't actually fix it.
        final inUse = (result.error ?? '').toLowerCase().contains('in use');
        setState(() {
          _error = result.error ?? 'Delete failed';
          _offerForce = !force && inUse;
        });
        return;
      }
      Navigator.of(context).pop(true);
    } catch (e) {
      if (mounted) setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Delete volume?'),
      content: SizedBox(
        width: 380,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              'This permanently deletes "${widget.volume.name}" from '
              '${widget.volume.serverName} and all data it holds. This '
              'cannot be undone.',
            ),
            const SizedBox(height: 16),
            Text(
              'Type "${widget.volume.name}" to confirm.',
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 4),
            TextField(
              controller: _confirmController,
              onChanged: (_) => setState(() {}),
              decoration: const InputDecoration(border: OutlineInputBorder()),
            ),
            if (_error != null) ...[
              const SizedBox(height: 12),
              Text(_error!, style: const TextStyle(color: AppColors.failed)),
              if (_offerForce) ...[
                const SizedBox(height: 4),
                const Text(
                  'This volume is still in use. Forcing removal may break '
                  'the container(s) using it.',
                  style: TextStyle(color: AppColors.warning),
                ),
              ],
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
          style: FilledButton.styleFrom(backgroundColor: AppColors.failed),
          onPressed: _loading || (!_nameMatches && !_offerForce)
              ? null
              : () => _submit(force: _offerForce),
          child: _loading
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text(_offerForce ? 'Force delete' : 'Delete'),
        ),
      ],
    );
  }
}
