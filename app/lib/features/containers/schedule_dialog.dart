import 'package:flutter/material.dart';

import '../../api/api_client.dart';

const _weekdayLabels = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
// cron day-of-week is 0=Sunday..6=Saturday; _weekdayLabels is Mon-first for
// a more natural UI, so index i (Mon=0) maps to cron day (i + 1) % 7.
int _cronDow(int labelIndex) => (labelIndex + 1) % 7;

/// Shows the add-schedule dialog for one container: either a recurring
/// weekly pattern (day checkboxes + a time of day, built into a standard
/// cron expression) or a one-time start/stop at a specific date and time.
/// Everything is entered in the viewer's local time and converted to UTC
/// before submitting — the backend stores and evaluates schedules in UTC.
/// Pops with true if a schedule was created.
Future<bool?> showScheduleDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required String serverId,
  required String containerId,
  required String containerName,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) => _ScheduleDialog(
      apiClient: apiClient,
      serverId: serverId,
      containerId: containerId,
      containerName: containerName,
    ),
  );
}

class _ScheduleDialog extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String containerName;

  const _ScheduleDialog({
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.containerName,
  });

  @override
  State<_ScheduleDialog> createState() => _ScheduleDialogState();
}

class _ScheduleDialogState extends State<_ScheduleDialog> {
  String _scheduleType = 'recurring';
  String _action = 'stop';
  final Set<int> _selectedDays = {0, 1, 2, 3, 4}; // Mon-Fri by default
  TimeOfDay _time = const TimeOfDay(hour: 20, minute: 0);
  DateTime? _oneTimeDate;
  TimeOfDay? _oneTimeTime;
  bool _loading = false;
  String? _error;

  Future<void> _pickTime() async {
    final picked = await showTimePicker(context: context, initialTime: _time);
    if (picked != null) setState(() => _time = picked);
  }

  Future<void> _pickOneTimeDate() async {
    final now = DateTime.now();
    final picked = await showDatePicker(
      context: context,
      initialDate: _oneTimeDate ?? now,
      firstDate: now,
      lastDate: now.add(const Duration(days: 365)),
    );
    if (picked != null) setState(() => _oneTimeDate = picked);
  }

  Future<void> _pickOneTimeTime() async {
    final picked = await showTimePicker(
      context: context,
      initialTime: _oneTimeTime ?? TimeOfDay.now(),
    );
    if (picked != null) setState(() => _oneTimeTime = picked);
  }

  Future<void> _submit() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      if (_scheduleType == 'recurring') {
        if (_selectedDays.isEmpty) {
          setState(() => _error = 'Select at least one day');
          return;
        }
        // Cron fields are evaluated in UTC on the backend, so the local
        // time+days picked here need converting to their UTC equivalent —
        // both the hour/minute and, since the UTC offset can push the
        // clock across midnight, which weekday(s) that lands on.
        final localNow = DateTime.now();
        final localSample = DateTime(
          localNow.year,
          localNow.month,
          localNow.day,
          _time.hour,
          _time.minute,
        );
        final utcSample = localSample.toUtc();
        final dayDelta =
            DateTime(utcSample.year, utcSample.month, utcSample.day)
                .difference(
                  DateTime(
                    localSample.year,
                    localSample.month,
                    localSample.day,
                  ),
                )
                .inDays;
        final utcDays = _selectedDays
            .map((d) => ((_cronDow(d) + dayDelta) % 7 + 7) % 7)
            .toSet();
        final cronExpr =
            '${utcSample.minute} ${utcSample.hour} * * ${(utcDays.toList()..sort()).join(',')}';

        await widget.apiClient.createSchedule(
          serverId: widget.serverId,
          containerId: widget.containerId,
          containerName: widget.containerName,
          action: _action,
          scheduleType: 'recurring',
          cronExpr: cronExpr,
        );
      } else {
        if (_oneTimeDate == null || _oneTimeTime == null) {
          setState(() => _error = 'Pick a date and time');
          return;
        }
        final local = DateTime(
          _oneTimeDate!.year,
          _oneTimeDate!.month,
          _oneTimeDate!.day,
          _oneTimeTime!.hour,
          _oneTimeTime!.minute,
        );
        await widget.apiClient.createSchedule(
          serverId: widget.serverId,
          containerId: widget.containerId,
          containerName: widget.containerName,
          action: _action,
          scheduleType: 'once',
          runOnceAt: local,
        );
      }
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() => _error = 'Failed to create schedule: $e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Add schedule'),
      content: SizedBox(
        width: 420,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(value: 'stop', label: Text('Stop')),
                  ButtonSegment(value: 'start', label: Text('Start')),
                ],
                selected: {_action},
                onSelectionChanged: (s) => setState(() => _action = s.first),
              ),
              const SizedBox(height: 12),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(value: 'recurring', label: Text('Recurring')),
                  ButtonSegment(value: 'once', label: Text('One-time')),
                ],
                selected: {_scheduleType},
                onSelectionChanged: (s) =>
                    setState(() => _scheduleType = s.first),
              ),
              const SizedBox(height: 16),
              if (_scheduleType == 'recurring') ...[
                Wrap(
                  spacing: 6,
                  children: [
                    for (var i = 0; i < _weekdayLabels.length; i++)
                      FilterChip(
                        label: Text(_weekdayLabels[i]),
                        selected: _selectedDays.contains(i),
                        onSelected: (selected) => setState(() {
                          if (selected) {
                            _selectedDays.add(i);
                          } else {
                            _selectedDays.remove(i);
                          }
                        }),
                      ),
                  ],
                ),
                const SizedBox(height: 12),
                ListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('Time'),
                  trailing: Text(_time.format(context)),
                  onTap: _pickTime,
                ),
              ] else ...[
                ListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('Date'),
                  trailing: Text(
                    _oneTimeDate == null
                        ? 'Pick a date'
                        : '${_oneTimeDate!.year}-${_oneTimeDate!.month.toString().padLeft(2, '0')}-${_oneTimeDate!.day.toString().padLeft(2, '0')}',
                  ),
                  onTap: _pickOneTimeDate,
                ),
                ListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('Time'),
                  trailing: Text(
                    _oneTimeTime?.format(context) ?? 'Pick a time',
                  ),
                  onTap: _pickOneTimeTime,
                ),
              ],
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!, style: const TextStyle(color: Colors.red)),
              ],
            ],
          ),
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
