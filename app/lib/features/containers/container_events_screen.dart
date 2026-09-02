import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/log_line.dart';

/// Bounded history of Docker events for one container (start/stop/die/
/// health_status/oom/...) — the troubleshooting "what happened here" view.
class ContainerEventsScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;

  const ContainerEventsScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.containerId,
  });

  @override
  State<ContainerEventsScreen> createState() => _ContainerEventsScreenState();
}

class _ContainerEventsScreenState extends State<ContainerEventsScreen> {
  late Future<List<ContainerEvent>> _future;

  @override
  void initState() {
    super.initState();
    _load();
  }

  void _load() {
    _future = widget.apiClient.listContainerEvents(
      widget.serverId,
      widget.containerId,
    );
  }

  IconData _iconFor(String action) {
    switch (action) {
      case 'start':
        return Icons.play_arrow;
      case 'stop':
      case 'die':
        return Icons.stop;
      case 'kill':
        return Icons.dangerous_outlined;
      case 'restart':
        return Icons.refresh;
      case 'pause':
        return Icons.pause;
      case 'unpause':
        return Icons.play_circle_outline;
      case 'health_status: healthy':
        return Icons.check_circle_outline;
      case 'health_status: unhealthy':
        return Icons.error_outline;
      case 'oom':
        return Icons.memory;
      case 'destroy':
        return Icons.delete_outline;
      default:
        return Icons.circle_outlined;
    }
  }

  Color? _colorFor(String action) {
    if (action.contains('unhealthy') ||
        action == 'die' ||
        action == 'oom' ||
        action == 'kill') {
      return Colors.redAccent;
    }
    if (action == 'start' || action.contains('health_status: healthy')) {
      return Colors.green;
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    return RefreshIndicator(
      onRefresh: () async {
        setState(_load);
        await _future;
      },
      child: FutureBuilder<List<ContainerEvent>>(
        future: _future,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return ListView(
              children: [
                Padding(
                  padding: const EdgeInsets.all(24),
                  child: Text('Failed to load events: ${snapshot.error}'),
                ),
              ],
            );
          }
          final events = snapshot.data ?? [];
          if (events.isEmpty) {
            return ListView(
              children: const [
                Padding(
                  padding: EdgeInsets.all(24),
                  child: Center(child: Text('No recent events')),
                ),
              ],
            );
          }
          // Most recent first.
          final sorted = events.reversed.toList();
          return ListView.separated(
            itemCount: sorted.length,
            separatorBuilder: (_, _) => const Divider(height: 1),
            itemBuilder: (context, index) {
              final e = sorted[index];
              return ListTile(
                dense: true,
                leading: Icon(_iconFor(e.action), color: _colorFor(e.action)),
                title: Text(e.action),
                subtitle: Text(e.timestamp.toLocal().toString()),
              );
            },
          );
        },
      ),
    );
  }
}
