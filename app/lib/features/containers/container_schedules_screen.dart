import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/schedule.dart';
import 'schedule_dialog.dart';
import '../../theme/app_theme.dart';

/// Lists a single container's start/stop schedules (recurring and
/// one-time), with an enable switch and delete per row, and an "Add
/// schedule" action that opens [showScheduleDialog].
class ContainerSchedulesScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String containerName;

  const ContainerSchedulesScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.containerName,
  });

  @override
  State<ContainerSchedulesScreen> createState() =>
      _ContainerSchedulesScreenState();
}

class _ContainerSchedulesScreenState extends State<ContainerSchedulesScreen> {
  late Future<List<Schedule>> _schedulesFuture;

  @override
  void initState() {
    super.initState();
    _schedulesFuture = _load();
  }

  Future<List<Schedule>> _load() {
    return widget.apiClient.listSchedules(containerId: widget.containerId);
  }

  void _refresh() {
    setState(() {
      _schedulesFuture = _load();
    });
  }

  Future<void> _addSchedule() async {
    final created = await showScheduleDialog(
      context,
      apiClient: widget.apiClient,
      serverId: widget.serverId,
      containerId: widget.containerId,
      containerName: widget.containerName,
    );
    if (created == true) _refresh();
  }

  Future<void> _toggle(Schedule schedule, bool enabled) async {
    try {
      await widget.apiClient.updateScheduleEnabled(schedule.id, enabled);
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to update schedule: $e')),
        );
      }
    }
  }

  Future<void> _delete(Schedule schedule) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Delete schedule?'),
        content: Text(
          'Remove the ${schedule.action} schedule for ${schedule.containerName}?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await widget.apiClient.deleteSchedule(schedule.id);
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to delete schedule: $e')),
        );
      }
    }
  }

  String _describe(Schedule s) {
    if (s.scheduleType == 'once') {
      return 'Once at ${s.runOnceAt?.toLocal()}';
    }
    return 'Recurring (${s.cronExpr}, UTC)';
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text('Schedules — ${widget.containerName}')),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _addSchedule,
        icon: const Icon(Icons.add),
        label: const Text('Add schedule'),
      ),
      body: FutureBuilder<List<Schedule>>(
        future: _schedulesFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(
              child: Text('Failed to load schedules: ${snapshot.error}'),
            );
          }
          final schedules = snapshot.data ?? [];
          if (schedules.isEmpty) {
            return const Center(
              child: Text('No schedules for this container.'),
            );
          }
          return ListView.builder(
            itemCount: schedules.length,
            itemBuilder: (context, index) {
              final s = schedules[index];
              return ListTile(
                leading: Icon(
                  s.action == 'start' ? Icons.play_arrow : Icons.stop,
                  color: s.enabled ? null : AppColors.neutral,
                ),
                title: Text(
                  '${s.action == 'start' ? 'Start' : 'Stop'} — ${_describe(s)}',
                ),
                subtitle: Text(
                  s.lastRunAt == null
                      ? 'Never run • next: ${s.nextRunAt.toLocal()}'
                      : 'Last run: ${s.lastRunAt!.toLocal()} (${s.lastRunStatus ?? '—'}) • next: ${s.nextRunAt.toLocal()}',
                ),
                trailing: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Switch(value: s.enabled, onChanged: (v) => _toggle(s, v)),
                    IconButton(
                      icon: const Icon(Icons.delete_outline),
                      tooltip: 'Delete',
                      onPressed: () => _delete(s),
                    ),
                  ],
                ),
              );
            },
          );
        },
      ),
    );
  }
}
