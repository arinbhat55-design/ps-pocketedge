import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../models/compose_file.dart';
import '../../models/deployment_event.dart';
import '../backups/backups_screen.dart';

class DeploymentStatusScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String deploymentId;
  // The deployment's service names, when known at launch time (e.g. from
  // the Compose file that was deployed) — enables "redeploy an individual
  // service". Empty hides that option rather than fetching it, since not
  // every caller has it on hand (a catalog-stack deploy, say).
  final List<String> serviceNames;
  // The Compose file this deployment was launched from, when known —
  // enables "roll back to version..." by fetching its version history.
  // Null hides that option (a catalog-stack deploy has no version
  // history to roll back through).
  final String? composeFileId;

  const DeploymentStatusScreen({
    super.key,
    required this.apiClient,
    required this.deploymentId,
    this.serviceNames = const [],
    this.composeFileId,
  });

  @override
  State<DeploymentStatusScreen> createState() => _DeploymentStatusScreenState();
}

class _DeploymentStatusScreenState extends State<DeploymentStatusScreen> {
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;
  final List<DeploymentEvent> _events = [];
  String? _error;
  bool _closed = false;
  bool _redeploying = false;
  bool _actionInProgress = false;
  // Stack start/stop/restart/remove replies over plain HTTP, not the
  // deploy-progress WebSocket (which the control plane closes once the
  // initial deploy reaches running/failed — see isTerminalPhase). This
  // tracks the outcome locally so the phase badge reflects it without
  // needing a stream event that will never arrive.
  String? _manualPhase;

  @override
  void initState() {
    super.initState();
    _connect();
  }

  void _connect() {
    final uri = widget.apiClient.deploymentStreamUri(widget.deploymentId);
    final channel = WebSocketChannel.connect(uri);
    _channel = channel;
    _sub = channel.stream.listen(
      (data) {
        final event = DeploymentEvent.fromJson(
          jsonDecode(data as String) as Map<String, dynamic>,
        );
        setState(() {
          _events.add(event);
          if (event.isTerminal) _closed = true;
        });
      },
      onError: (Object e) {
        setState(() => _error = 'Connection error: $e');
      },
      onDone: () {
        setState(() => _closed = true);
      },
    );
  }

  @override
  void dispose() {
    _sub?.cancel();
    _channel?.sink.close();
    super.dispose();
  }

  Future<void> _redeploy() async {
    setState(() => _redeploying = true);
    try {
      await widget.apiClient.redeployDeployment(widget.deploymentId);
      await _resetAndReconnect();
    } catch (e) {
      setState(() {
        _error = 'Failed to redeploy: $e';
      });
    } finally {
      if (mounted) setState(() => _redeploying = false);
    }
  }

  Future<void> _redeployService() async {
    final serviceName = await showDialog<String>(
      context: context,
      builder: (_) => SimpleDialog(
        title: const Text('Redeploy which service?'),
        children: [
          for (final s in widget.serviceNames)
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop(s),
              child: Text(s),
            ),
        ],
      ),
    );
    if (serviceName == null || !mounted) return;

    setState(() => _actionInProgress = true);
    try {
      final result = await widget.apiClient.redeployService(
        widget.deploymentId,
        serviceName,
      );
      final ok = result['success'] as bool? ?? false;
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              ok
                  ? 'Service "$serviceName" redeployed.'
                  : 'Failed to redeploy "$serviceName": ${result['error']}',
            ),
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to redeploy "$serviceName": $e')),
        );
      }
    } finally {
      if (mounted) setState(() => _actionInProgress = false);
    }
  }

  Future<void> _rollback() async {
    final composeFileId = widget.composeFileId;
    if (composeFileId == null) return;

    List<ComposeFileVersionSummary> versions;
    try {
      versions = await widget.apiClient.listComposeFileVersions(composeFileId);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to load version history: $e')),
        );
      }
      return;
    }
    if (!mounted) return;
    if (versions.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('No past versions to roll back to yet.'),
        ),
      );
      return;
    }

    final version = await showDialog<ComposeFileVersionSummary>(
      context: context,
      builder: (_) => SimpleDialog(
        title: const Text('Roll back to which version?'),
        children: [
          for (final v in versions)
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop(v),
              child: Text('v${v.versionNumber} — ${v.createdAt.toLocal()}'),
            ),
        ],
      ),
    );
    if (version == null || !mounted) return;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('Roll back to v${version.versionNumber}?'),
        content: const Text(
          'This redeploys the whole stack using that version\'s Compose '
          'content. The Compose file itself is unchanged.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Roll back'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    setState(() => _redeploying = true);
    try {
      await widget.apiClient.rollbackDeployment(widget.deploymentId, version.id);
      await _resetAndReconnect();
    } catch (e) {
      setState(() {
        _error = 'Failed to roll back: $e';
      });
    } finally {
      if (mounted) setState(() => _redeploying = false);
    }
  }

  Future<void> _resetAndReconnect() async {
    await _sub?.cancel();
    await _channel?.sink.close();
    if (!mounted) return;
    setState(() {
      _events.clear();
      _error = null;
      _closed = false;
      _manualPhase = null;
    });
    _connect();
  }

  Future<void> _openBackups() async {
    final restoreTriggered = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => BackupsScreen(
          apiClient: widget.apiClient,
          deploymentId: widget.deploymentId,
        ),
      ),
    );
    if (restoreTriggered == true) {
      await _resetAndReconnect();
    }
  }

  Future<void> _runStackAction(String action) async {
    if (action == 'remove') {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (_) => AlertDialog(
          title: const Text('Remove this stack?'),
          content: const Text(
            'This stops and removes every container in this deployment and '
            'its network. Named volumes are kept.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('Cancel'),
            ),
            FilledButton(
              style: FilledButton.styleFrom(
                backgroundColor: Theme.of(context).colorScheme.error,
              ),
              onPressed: () => Navigator.of(context).pop(true),
              child: const Text('Remove'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
    }

    setState(() => _actionInProgress = true);
    try {
      final result = await widget.apiClient.deploymentAction(
        widget.deploymentId,
        action,
      );
      final ok = action == 'remove'
          ? (result['success'] as bool? ?? false)
          : (result['results'] as List<dynamic>? ?? []).every(
              (r) => (r as Map<String, dynamic>)['success'] == true,
            );
      if (mounted) {
        setState(() => _manualPhase = ok ? _phaseForAction(action) : 'failed');
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              ok
                  ? 'Stack $action succeeded.'
                  : 'Stack $action failed for one or more containers.',
            ),
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to $action stack: $e')));
      }
    } finally {
      if (mounted) setState(() => _actionInProgress = false);
    }
  }

  String _phaseForAction(String action) {
    switch (action) {
      case 'start':
      case 'restart':
        return 'running';
      case 'stop':
        return 'stopped';
      case 'remove':
        return 'removed';
      default:
        return action;
    }
  }

  Color _phaseColor(String phase) {
    switch (phase) {
      case 'running':
        return Colors.green;
      case 'failed':
        return Colors.red;
      case 'pending':
        return Colors.grey;
      case 'stopped':
        return Colors.blueGrey;
      case 'removed':
        return Colors.grey;
      default:
        return Colors.orange;
    }
  }

  @override
  Widget build(BuildContext context) {
    final latestPhase =
        _manualPhase ?? (_events.isEmpty ? 'connecting' : _events.last.phase);

    return Scaffold(
      appBar: AppBar(
        title: const Text('Deployment status'),
        actions: [
          IconButton(
            icon: const Icon(Icons.backup),
            tooltip: 'Backups',
            onPressed: _openBackups,
          ),
          if (_closed)
            IconButton(
              icon: _redeploying
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.replay),
              tooltip: 'Redeploy',
              onPressed: _redeploying ? null : _redeploy,
            ),
          if (_closed)
            PopupMenuButton<String>(
              enabled: !_actionInProgress,
              tooltip: 'Stack actions',
              icon: _actionInProgress
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.settings_outlined),
              onSelected: (value) {
                switch (value) {
                  case 'redeploy-service':
                    _redeployService();
                  case 'rollback':
                    _rollback();
                  default:
                    _runStackAction(value);
                }
              },
              itemBuilder: (context) => [
                const PopupMenuItem(
                  value: 'start',
                  child: Text('Start stack'),
                ),
                const PopupMenuItem(value: 'stop', child: Text('Stop stack')),
                const PopupMenuItem(
                  value: 'restart',
                  child: Text('Restart stack'),
                ),
                const PopupMenuItem(
                  value: 'remove',
                  child: Text('Remove stack'),
                ),
                if (widget.serviceNames.isNotEmpty || widget.composeFileId != null)
                  const PopupMenuDivider(),
                if (widget.serviceNames.isNotEmpty)
                  const PopupMenuItem(
                    value: 'redeploy-service',
                    child: Text('Redeploy a service...'),
                  ),
                if (widget.composeFileId != null)
                  const PopupMenuItem(
                    value: 'rollback',
                    child: Text('Roll back to version...'),
                  ),
              ],
            ),
        ],
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                Icon(Icons.circle, size: 12, color: _phaseColor(latestPhase)),
                const SizedBox(width: 8),
                Text(
                  latestPhase.toUpperCase(),
                  style: Theme.of(context).textTheme.titleMedium,
                ),
                if (!_closed && _error == null) ...[
                  const SizedBox(width: 12),
                  const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                ],
              ],
            ),
          ),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Text(_error!, style: const TextStyle(color: Colors.red)),
            ),
          const Divider(height: 1),
          Expanded(
            child: _events.isEmpty
                ? const Center(child: CircularProgressIndicator())
                : ListView.builder(
                    itemCount: _events.length,
                    itemBuilder: (context, index) {
                      final e = _events[index];
                      return ListTile(
                        leading: Icon(
                          Icons.circle,
                          size: 10,
                          color: _phaseColor(e.phase),
                        ),
                        title: Text(e.phase),
                        subtitle: e.message.isEmpty ? null : Text(e.message),
                        trailing: Text(
                          '${e.createdAt.toLocal().hour.toString().padLeft(2, '0')}:'
                          '${e.createdAt.toLocal().minute.toString().padLeft(2, '0')}:'
                          '${e.createdAt.toLocal().second.toString().padLeft(2, '0')}',
                          style: Theme.of(context).textTheme.bodySmall,
                        ),
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }
}
