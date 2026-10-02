import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/resource_insights.dart';
import '../../theme/app_theme.dart';

/// Shows the create/edit dialog for a container alert rule ([rule] null =
/// create). [servers] maps server id → name for the scope picker. Pops
/// with true once saved.
Future<bool?> showAlertRuleDialog(
  BuildContext context, {
  required ApiClient apiClient,
  required Map<String, String> servers,
  ContainerAlertRule? rule,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) =>
        _AlertRuleDialog(apiClient: apiClient, servers: servers, rule: rule),
  );
}

/// Starting threshold per metric, as typed in the field: 0.9 cores,
/// 90% of the memory limit, 500 processes.
const _defaultThreshold = {'cpu': '0.9', 'memory': '90', 'pids': '500'};

/// A stored threshold as the field shows it — CPU converted from the
/// API's percent-of-one-core to cores, trailing zeros dropped.
String _thresholdText(String metric, double threshold) {
  final v = metric == 'cpu' ? threshold / 100 : threshold;
  return v == v.roundToDouble()
      ? v.toStringAsFixed(0)
      : v.toStringAsFixed(2).replaceFirst(RegExp(r'0+$'), '');
}

class _AlertRuleDialog extends StatefulWidget {
  final ApiClient apiClient;
  final Map<String, String> servers;
  final ContainerAlertRule? rule;

  const _AlertRuleDialog({
    required this.apiClient,
    required this.servers,
    this.rule,
  });

  @override
  State<_AlertRuleDialog> createState() => _AlertRuleDialogState();
}

class _AlertRuleDialogState extends State<_AlertRuleDialog> {
  late final _nameController = TextEditingController(
    text: widget.rule?.name ?? '',
  );
  late final _thresholdController = TextEditingController(
    text: widget.rule == null
        ? _defaultThreshold['cpu']
        : _thresholdText(widget.rule!.metric, widget.rule!.threshold),
  );
  late final _durationController = TextEditingController(
    text: '${(widget.rule?.durationSeconds ?? 300) ~/ 60}',
  );
  late final _containerController = TextEditingController(
    text: widget.rule?.containerName ?? '',
  );
  late String _metric = widget.rule?.metric ?? 'cpu';
  late String _severity = widget.rule?.severity ?? 'warning';
  late String? _serverId = widget.servers.containsKey(widget.rule?.serverId)
      ? widget.rule?.serverId
      : null;
  bool _saving = false;
  String? _error;

  @override
  void dispose() {
    _nameController.dispose();
    _thresholdController.dispose();
    _durationController.dispose();
    _containerController.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final name = _nameController.text.trim();
    final threshold = double.tryParse(_thresholdController.text.trim());
    final minutes = int.tryParse(_durationController.text.trim());
    if (name.isEmpty) {
      setState(() => _error = 'Give the rule a name.');
      return;
    }
    if (threshold == null || threshold < 0) {
      setState(() => _error = 'Threshold must be a non-negative number.');
      return;
    }
    if (_metric == 'memory' && threshold > 100) {
      setState(() => _error = 'Memory is a percentage of the limit (0–100).');
      return;
    }
    if (minutes == null || minutes < 0 || minutes > 180) {
      setState(() => _error = 'Duration must be 0–180 minutes.');
      return;
    }
    final container = _containerController.text.trim();
    final rule = ContainerAlertRule(
      id: widget.rule?.id ?? '',
      name: name,
      serverId: _serverId,
      containerName: container.isEmpty ? null : container,
      metric: _metric,
      // The API takes CPU as percent of one core; the field is in cores.
      threshold: _metric == 'cpu' ? threshold * 100 : threshold,
      durationSeconds: minutes * 60,
      severity: _severity,
      enabled: widget.rule?.enabled ?? true,
    );
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      if (widget.rule == null) {
        await widget.apiClient.createContainerAlertRule(rule);
      } else {
        await widget.apiClient.updateContainerAlertRule(rule);
      }
      if (mounted) Navigator.of(context).pop(true);
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    } catch (e) {
      setState(() => _error = 'Couldn\'t save rule: $e');
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final unit = switch (_metric) {
      'pids' => 'processes',
      'cpu' => 'cores',
      _ => '%',
    };
    return AlertDialog(
      title: Text(widget.rule == null ? 'New alert rule' : 'Edit alert rule'),
      content: SizedBox(
        width: 400,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TextField(
                controller: _nameController,
                decoration: const InputDecoration(
                  labelText: 'Name',
                  hintText: 'e.g. High CPU',
                ),
              ),
              const SizedBox(height: Space.md),
              DropdownButtonFormField<String>(
                initialValue: _metric,
                decoration: const InputDecoration(labelText: 'Metric'),
                items: const [
                  DropdownMenuItem(value: 'cpu', child: Text('CPU (cores)')),
                  DropdownMenuItem(
                    value: 'memory',
                    child: Text('Memory (% of limit)'),
                  ),
                  DropdownMenuItem(value: 'pids', child: Text('Process count')),
                ],
                onChanged: (v) => setState(() {
                  final next = v ?? _metric;
                  // Swap in the new metric's default unless the user
                  // already typed their own threshold.
                  if (_thresholdController.text.trim() ==
                      _defaultThreshold[_metric]) {
                    _thresholdController.text = _defaultThreshold[next]!;
                  }
                  _metric = next;
                }),
              ),
              const SizedBox(height: Space.md),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _thresholdController,
                      keyboardType: const TextInputType.numberWithOptions(
                        decimal: true,
                      ),
                      decoration: InputDecoration(
                        labelText: 'Above',
                        suffixText: unit,
                      ),
                    ),
                  ),
                  const SizedBox(width: Space.md),
                  Expanded(
                    child: TextField(
                      controller: _durationController,
                      keyboardType: TextInputType.number,
                      decoration: const InputDecoration(
                        labelText: 'For at least',
                        suffixText: 'min',
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: Space.md),
              DropdownButtonFormField<String?>(
                initialValue: _serverId,
                decoration: const InputDecoration(labelText: 'Server'),
                items: [
                  const DropdownMenuItem(
                    value: null,
                    child: Text('All servers'),
                  ),
                  for (final e in widget.servers.entries)
                    DropdownMenuItem(value: e.key, child: Text(e.value)),
                ],
                onChanged: (v) => setState(() => _serverId = v),
              ),
              const SizedBox(height: Space.md),
              TextField(
                controller: _containerController,
                decoration: const InputDecoration(
                  labelText: 'Container name (blank = all containers)',
                  hintText: 'Matched exactly; survives recreates',
                ),
              ),
              const SizedBox(height: Space.md),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(value: 'warning', label: Text('Warning')),
                  ButtonSegment(value: 'critical', label: Text('Critical')),
                ],
                selected: {_severity},
                onSelectionChanged: (s) => setState(() => _severity = s.first),
              ),
              if (_error != null) ...[
                const SizedBox(height: Space.md),
                Text(_error!, style: const TextStyle(color: AppColors.failed)),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: _saving ? null : () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _saving ? null : _save,
          child: _saving
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Save'),
        ),
      ],
    );
  }
}
