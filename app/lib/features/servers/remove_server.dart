import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../theme/app_theme.dart';

/// Asks for confirmation, then retires the server. Returns true once it's
/// removed; on failure shows the control plane's reason in a snackbar and
/// returns false. Shared by the Servers list card and the server detail
/// page so both explain removal the same way.
Future<bool> confirmAndRemoveServer(
  BuildContext context, {
  required ApiClient apiClient,
  required String serverId,
  required String serverName,
}) async {
  final confirmed = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text('Remove $serverName?'),
      content: const Text(
        '• It disappears from Servers and Containers.\n'
        '• Its container snapshot, metric history and container '
        'schedules are deleted.\n'
        '• Past deployments keep a record of which server they ran on.\n'
        '• Its agent can no longer connect. To add this machine back, '
        'enroll it again with Add server.',
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          style: FilledButton.styleFrom(backgroundColor: AppColors.failed),
          onPressed: () => Navigator.of(context).pop(true),
          child: const Text('Remove server'),
        ),
      ],
    ),
  );
  if (confirmed != true || !context.mounted) return false;

  try {
    await apiClient.removeServer(serverId);
    return true;
  } catch (e) {
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            'Couldn\'t remove server: ${e is ApiException ? e.message : e}',
          ),
        ),
      );
    }
    return false;
  }
}

/// The "⋮" menu with Remove server. Disabled, with the reason, while the
/// server is still connected.
class ServerMenuButton extends StatelessWidget {
  final bool canRemove;
  final VoidCallback onRemove;
  // Smaller hit area for use inside a card header.
  final bool dense;

  const ServerMenuButton({
    super.key,
    required this.canRemove,
    required this.onRemove,
    this.dense = false,
  });

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<String>(
      tooltip: 'More',
      icon: Icon(Icons.more_vert, size: dense ? 20 : 24),
      padding: dense ? EdgeInsets.zero : const EdgeInsets.all(8),
      onSelected: (v) {
        if (v == 'remove') onRemove();
      },
      itemBuilder: (context) => [
        PopupMenuItem(
          value: 'remove',
          enabled: canRemove,
          child: ListTile(
            contentPadding: EdgeInsets.zero,
            leading: Icon(
              Icons.delete_outline,
              color: canRemove ? AppColors.failed : null,
            ),
            title: Text(
              'Remove server',
              style: canRemove
                  ? const TextStyle(color: AppColors.failed)
                  : null,
            ),
            subtitle: canRemove
                ? null
                : const Text('Only disconnected servers can be removed'),
          ),
        ),
      ],
    );
  }
}
