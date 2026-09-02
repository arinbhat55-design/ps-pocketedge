import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import 'container_config_form.dart';

/// Shows the recreate-container sheet, pre-filled from [current] (the
/// container's cheap [ContainerInfo] fields, already loaded on the detail
/// screen) and [detail] (its expensive inspected fields — env, restart
/// policy). Stops and removes [containerId] and creates a fresh one in its
/// place from the edited config. Pops with the new container's id on
/// success, or null if cancelled.
Future<String?> showRecreateContainerDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required String serverId,
  required String containerId,
  required ContainerConfig current,
}) {
  return showModalBottomSheet<String>(
    context: context,
    isScrollControlled: true,
    builder: (_) => _RecreateContainerSheet(
      apiClient: apiClient,
      serverId: serverId,
      containerId: containerId,
      current: current,
    ),
  );
}

class _RecreateContainerSheet extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final ContainerConfig current;

  const _RecreateContainerSheet({
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.current,
  });

  @override
  State<_RecreateContainerSheet> createState() =>
      _RecreateContainerSheetState();
}

class _RecreateContainerSheetState extends State<_RecreateContainerSheet> {
  bool _loading = false;
  String? _error;

  Future<void> _submit(ContainerConfig config) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await widget.apiClient.recreateContainer(
        widget.serverId,
        widget.containerId,
        config,
      );
      if (!result.success) {
        setState(() => _error = result.error ?? 'Failed to recreate container');
        return;
      }
      if (mounted) Navigator.of(context).pop(result.containerId);
    } catch (e) {
      setState(() => _error = 'Failed to recreate container: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ContainerConfigForm(
      title: 'Recreate container',
      submitLabel: 'Recreate',
      initial: widget.current,
      loading: _loading,
      error: _error,
      header: const Text(
        'The existing container is stopped and removed, then a new one is '
        'created from the configuration below.',
        style: TextStyle(fontStyle: FontStyle.italic),
      ),
      onSubmit: _submit,
      apiClient: widget.apiClient,
      serverId: widget.serverId,
      excludeContainerId: widget.containerId,
    );
  }
}
