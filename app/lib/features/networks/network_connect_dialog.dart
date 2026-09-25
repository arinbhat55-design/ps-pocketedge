import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import '../../theme/app_theme.dart';

/// Shows a dialog to pick one of [serverId]'s containers and attach it to
/// [networkId]/[networkName]. Pops with `true` on a successful connect.
Future<bool?> showNetworkConnectDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required String serverId,
  required String networkId,
  required String networkName,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) => _NetworkConnectDialog(
      apiClient: apiClient,
      serverId: serverId,
      networkId: networkId,
      networkName: networkName,
    ),
  );
}

class _NetworkConnectDialog extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String networkId;
  final String networkName;

  const _NetworkConnectDialog({
    required this.apiClient,
    required this.serverId,
    required this.networkId,
    required this.networkName,
  });

  @override
  State<_NetworkConnectDialog> createState() => _NetworkConnectDialogState();
}

class _NetworkConnectDialogState extends State<_NetworkConnectDialog> {
  late final Future<List<FleetContainer>> _containersFuture = widget.apiClient
      .listContainers(serverId: widget.serverId);

  String? _selectedContainerId;
  bool _loading = false;
  String? _error;

  Future<void> _submit() async {
    final containerId = _selectedContainerId;
    if (containerId == null) {
      setState(() => _error = 'Select a container');
      return;
    }

    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await widget.apiClient.connectContainerToNetwork(
        widget.serverId,
        widget.networkId,
        containerId,
      );
      if (!mounted) return;
      if (!result.success) {
        setState(() => _error = result.error ?? 'Connect failed');
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
      title: Text('Connect a container to ${widget.networkName}'),
      content: SizedBox(
        width: 360,
        child: FutureBuilder<List<FleetContainer>>(
          future: _containersFuture,
          builder: (context, snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return const Padding(
                padding: EdgeInsets.all(16),
                child: Center(child: CircularProgressIndicator()),
              );
            }
            final containers = snapshot.data ?? [];
            return Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                if (containers.isEmpty)
                  const Text('No containers found on this server.')
                else
                  DropdownButtonFormField<String>(
                    initialValue: _selectedContainerId,
                    decoration: const InputDecoration(labelText: 'Container'),
                    items: [
                      for (final c in containers)
                        DropdownMenuItem(
                          value: c.container.containerId,
                          child: Text(c.container.name),
                        ),
                    ],
                    onChanged: (value) =>
                        setState(() => _selectedContainerId = value),
                  ),
                if (_error != null) ...[
                  const SizedBox(height: 12),
                  Text(_error!, style: const TextStyle(color: AppColors.failed)),
                ],
              ],
            );
          },
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
              : const Text('Connect'),
        ),
      ],
    );
  }
}
