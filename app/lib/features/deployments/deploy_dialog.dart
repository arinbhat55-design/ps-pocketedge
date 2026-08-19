import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/stack.dart';

/// Lets the user pick a stack to deploy to [serverName]/[serverId].
/// Pops with the new deployment's ID on success, or null if cancelled.
class DeployDialog extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String serverName;

  const DeployDialog({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.serverName,
  });

  @override
  State<DeployDialog> createState() => _DeployDialogState();
}

class _DeployDialogState extends State<DeployDialog> {
  Future<List<StackSummary>>? _stacksFuture;
  StackSummary? _selected;
  bool _deploying = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _stacksFuture = widget.apiClient.listStacks();
  }

  Future<void> _deploy() async {
    if (_selected == null) return;
    setState(() {
      _deploying = true;
      _error = null;
    });
    try {
      final deploymentId = await widget.apiClient.createDeployment(
        stackId: _selected!.id,
        serverId: widget.serverId,
      );
      if (mounted) Navigator.of(context).pop(deploymentId);
    } catch (e) {
      setState(() {
        _error = 'Failed to start deployment: $e';
        _deploying = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('Deploy to ${widget.serverName}'),
      content: SizedBox(
        width: 400,
        child: FutureBuilder<List<StackSummary>>(
          future: _stacksFuture,
          builder: (context, snapshot) {
            if (snapshot.connectionState == ConnectionState.waiting) {
              return const Center(
                child: Padding(
                  padding: EdgeInsets.all(24),
                  child: CircularProgressIndicator(),
                ),
              );
            }
            if (snapshot.hasError) {
              return Text('Failed to load stacks: ${snapshot.error}');
            }
            final stacks = snapshot.data ?? [];
            if (stacks.isEmpty) {
              return const Text('No stacks available.');
            }
            _selected ??= stacks.first;
            return Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                DropdownButtonFormField<StackSummary>(
                  initialValue: _selected,
                  decoration: const InputDecoration(labelText: 'StackSummary'),
                  items: stacks
                      .map((s) => DropdownMenuItem(
                            value: s,
                            child: Text(s.name),
                          ))
                      .toList(),
                  onChanged: (v) => setState(() => _selected = v),
                ),
                if (_error != null) ...[
                  const SizedBox(height: 12),
                  Text(_error!, style: const TextStyle(color: Colors.red)),
                ],
              ],
            );
          },
        ),
      ),
      actions: [
        TextButton(
          onPressed: _deploying ? null : () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: (_selected == null || _deploying) ? null : _deploy,
          child: _deploying
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Deploy'),
        ),
      ],
    );
  }
}
