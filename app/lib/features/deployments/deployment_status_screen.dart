import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../models/deployment_event.dart';

class DeploymentStatusScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String deploymentId;

  const DeploymentStatusScreen({
    super.key,
    required this.apiClient,
    required this.deploymentId,
  });

  @override
  State<DeploymentStatusScreen> createState() =>
      _DeploymentStatusScreenState();
}

class _DeploymentStatusScreenState extends State<DeploymentStatusScreen> {
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;
  final List<DeploymentEvent> _events = [];
  String? _error;
  bool _closed = false;

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
            jsonDecode(data as String) as Map<String, dynamic>);
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

  Color _phaseColor(String phase) {
    switch (phase) {
      case 'running':
        return Colors.green;
      case 'failed':
        return Colors.red;
      case 'pending':
        return Colors.grey;
      default:
        return Colors.orange;
    }
  }

  @override
  Widget build(BuildContext context) {
    final latestPhase = _events.isEmpty ? 'connecting' : _events.last.phase;

    return Scaffold(
      appBar: AppBar(title: const Text('Deployment status')),
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
                        leading: Icon(Icons.circle,
                            size: 10, color: _phaseColor(e.phase)),
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
