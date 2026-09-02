import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import '../../models/server.dart';
import 'container_config_form.dart';

/// Shows the create-container sheet. If [serverId] is omitted (fleet-wide
/// entry point), the sheet includes a server picker; when opened from a
/// specific server's context, [serverId]/[serverName] are pre-filled and
/// no picker is shown. Pops with the new container's id on success, or
/// null if cancelled.
Future<String?> showCreateContainerDialog(
  BuildContext context, {
  required ApiClient apiClient,
  String? serverId,
  String? serverName,
  String? initialImage,
}) {
  return showModalBottomSheet<String>(
    context: context,
    isScrollControlled: true,
    builder: (_) => _CreateContainerSheet(
      apiClient: apiClient,
      presetServerId: serverId,
      presetServerName: serverName,
      initialImage: initialImage,
    ),
  );
}

class _CreateContainerSheet extends StatefulWidget {
  final ApiClient apiClient;
  final String? presetServerId;
  final String? presetServerName;
  final String? initialImage;

  const _CreateContainerSheet({
    required this.apiClient,
    this.presetServerId,
    this.presetServerName,
    this.initialImage,
  });

  @override
  State<_CreateContainerSheet> createState() => _CreateContainerSheetState();
}

class _CreateContainerSheetState extends State<_CreateContainerSheet> {
  late final Future<List<Server>>? _serversFuture =
      widget.presetServerId == null ? widget.apiClient.listServers() : null;
  String? _selectedServerId;
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _selectedServerId = widget.presetServerId;
  }

  Future<void> _submit(ContainerConfig config) async {
    final serverId = _selectedServerId;
    if (serverId == null) {
      setState(() => _error = 'Select a server');
      return;
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await widget.apiClient.createContainer(serverId, config);
      if (!result.success) {
        setState(() => _error = result.error ?? 'Failed to create container');
        return;
      }
      if (mounted) Navigator.of(context).pop(result.containerId);
    } catch (e) {
      setState(() => _error = 'Failed to create container: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    Widget? header;
    if (widget.presetServerId != null) {
      header = Text(
        'Server: ${widget.presetServerName}',
        style: Theme.of(context).textTheme.bodyMedium,
      );
    } else {
      header = FutureBuilder<List<Server>>(
        future: _serversFuture,
        builder: (context, snapshot) {
          final servers = snapshot.data ?? [];
          return DropdownButtonFormField<String>(
            initialValue: _selectedServerId,
            decoration: const InputDecoration(labelText: 'Server'),
            items: [
              for (final s in servers)
                DropdownMenuItem(value: s.id, child: Text(s.name)),
            ],
            onChanged: (v) => setState(() => _selectedServerId = v),
          );
        },
      );
    }

    return ContainerConfigForm(
      title: 'Create container',
      submitLabel: 'Create',
      initial: widget.initialImage == null
          ? null
          : ContainerConfig(image: widget.initialImage!, name: ''),
      loading: _loading,
      error: _error,
      header: header,
      onSubmit: _submit,
      apiClient: widget.apiClient,
      serverId: _selectedServerId,
    );
  }
}
