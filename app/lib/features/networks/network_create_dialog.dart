import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/server.dart';

/// Shows the create-network dialog: pick a server (unless preset), a name,
/// an optional driver, and whether the network should be internal-only.
/// Pops with `true` on a successful create.
Future<bool?> showCreateNetworkDialog(
  BuildContext context, {
  required ApiClient apiClient,
  String? serverId,
  String? serverName,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) => _CreateNetworkDialog(
      apiClient: apiClient,
      presetServerId: serverId,
      presetServerName: serverName,
    ),
  );
}

class _CreateNetworkDialog extends StatefulWidget {
  final ApiClient apiClient;
  final String? presetServerId;
  final String? presetServerName;

  const _CreateNetworkDialog({
    required this.apiClient,
    this.presetServerId,
    this.presetServerName,
  });

  @override
  State<_CreateNetworkDialog> createState() => _CreateNetworkDialogState();
}

class _CreateNetworkDialogState extends State<_CreateNetworkDialog> {
  late final Future<List<Server>>? _serversFuture =
      widget.presetServerId == null ? widget.apiClient.listServers() : null;

  String? _selectedServerId;
  final _nameController = TextEditingController();
  String _driver = 'bridge';
  bool _internal = false;
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _selectedServerId = widget.presetServerId;
  }

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final serverId = _selectedServerId;
    final name = _nameController.text.trim();
    if (serverId == null || name.isEmpty) {
      setState(() => _error = 'Server and name are required');
      return;
    }

    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await widget.apiClient.createNetwork(
        serverId,
        name: name,
        driver: _driver,
        internal: _internal,
      );
      if (!mounted) return;
      if (!result.success) {
        setState(() => _error = result.error ?? 'Create failed');
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
      title: const Text('Create network'),
      content: SizedBox(
        width: 360,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (widget.presetServerId != null)
              Text('Server: ${widget.presetServerName}')
            else
              FutureBuilder<List<Server>>(
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
                    onChanged: (value) =>
                        setState(() => _selectedServerId = value),
                  );
                },
              ),
            const SizedBox(height: 12),
            TextField(
              controller: _nameController,
              decoration: const InputDecoration(labelText: 'Name'),
            ),
            const SizedBox(height: 12),
            DropdownButtonFormField<String>(
              initialValue: _driver,
              decoration: const InputDecoration(labelText: 'Driver'),
              items: const [
                DropdownMenuItem(value: 'bridge', child: Text('bridge')),
                DropdownMenuItem(value: 'overlay', child: Text('overlay')),
                DropdownMenuItem(value: 'macvlan', child: Text('macvlan')),
              ],
              onChanged: (value) => setState(() => _driver = value ?? 'bridge'),
            ),
            const SizedBox(height: 4),
            CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              controlAffinity: ListTileControlAffinity.leading,
              title: const Text('Internal (no external connectivity)'),
              value: _internal,
              onChanged: (v) => setState(() => _internal = v ?? false),
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
              : const Text('Create'),
        ),
      ],
    );
  }
}
