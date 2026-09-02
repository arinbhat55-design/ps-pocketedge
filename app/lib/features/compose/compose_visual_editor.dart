import 'package:flutter/material.dart';

import '../../models/compose_file.dart';

const _restartPolicies = ['no', 'always', 'on-failure', 'unless-stopped'];

class _EnvRow {
  final TextEditingController key;
  final TextEditingController value;
  _EnvRow({String key = '', String value = ''})
    : key = TextEditingController(text: key),
      value = TextEditingController(text: value);
  void dispose() {
    key.dispose();
    value.dispose();
  }
}

class _ListRow {
  final TextEditingController text;
  _ListRow({String text = ''}) : text = TextEditingController(text: text);
  void dispose() => text.dispose();
}

class _ServiceState {
  final TextEditingController name;
  final TextEditingController image;
  final TextEditingController command;
  String restart;
  final List<_ListRow> ports;
  final List<_ListRow> volumes;
  final List<_EnvRow> env;
  // Displayed in cores (e.g. "1.5") and MB, matching
  // container_config_form.dart's resource-limit fields exactly, then
  // converted to nanoCPUs/bytes in toDraft().
  final TextEditingController cpuLimit;
  final TextEditingController memoryLimit;
  final TextEditingController memoryReservation;
  // Healthcheck: test is a shell command (CMD-SHELL); the rest are in
  // seconds, blank meaning "use Compose's default" for that one field.
  final TextEditingController healthCheckTest;
  final TextEditingController healthCheckInterval;
  final TextEditingController healthCheckTimeout;
  final TextEditingController healthCheckRetries;
  final TextEditingController healthCheckStartPeriod;

  _ServiceState(ComposeServiceDraft draft)
    : name = TextEditingController(text: draft.name),
      image = TextEditingController(text: draft.image),
      command = TextEditingController(text: draft.command),
      restart = draft.restart.isEmpty ? 'no' : draft.restart,
      ports = [for (final p in draft.ports) _ListRow(text: p)],
      volumes = [for (final v in draft.volumes) _ListRow(text: v)],
      env = [
        for (final e in draft.environment.entries)
          _EnvRow(key: e.key, value: e.value),
      ],
      cpuLimit = TextEditingController(
        text: draft.nanoCpus == 0 ? '' : '${draft.nanoCpus / 1e9}',
      ),
      memoryLimit = TextEditingController(
        text: draft.memoryLimitBytes == 0
            ? ''
            : '${draft.memoryLimitBytes ~/ (1024 * 1024)}',
      ),
      memoryReservation = TextEditingController(
        text: draft.memoryReservationBytes == 0
            ? ''
            : '${draft.memoryReservationBytes ~/ (1024 * 1024)}',
      ),
      healthCheckTest = TextEditingController(text: draft.healthCheckTest),
      healthCheckInterval = TextEditingController(
        text: draft.healthCheckIntervalSeconds == 0
            ? ''
            : '${draft.healthCheckIntervalSeconds}',
      ),
      healthCheckTimeout = TextEditingController(
        text: draft.healthCheckTimeoutSeconds == 0
            ? ''
            : '${draft.healthCheckTimeoutSeconds}',
      ),
      healthCheckRetries = TextEditingController(
        text: draft.healthCheckRetries == 0
            ? ''
            : '${draft.healthCheckRetries}',
      ),
      healthCheckStartPeriod = TextEditingController(
        text: draft.healthCheckStartPeriodSeconds == 0
            ? ''
            : '${draft.healthCheckStartPeriodSeconds}',
      );

  ComposeServiceDraft toDraft() {
    return ComposeServiceDraft(
      name: name.text.trim(),
      image: image.text.trim(),
      command: command.text.trim(),
      restart: restart == 'no' ? '' : restart,
      ports: [
        for (final p in ports)
          if (p.text.text.trim().isNotEmpty) p.text.text.trim(),
      ],
      volumes: [
        for (final v in volumes)
          if (v.text.text.trim().isNotEmpty) v.text.text.trim(),
      ],
      environment: {
        for (final e in env)
          if (e.key.text.trim().isNotEmpty) e.key.text.trim(): e.value.text,
      },
      nanoCpus: cpuLimit.text.trim().isEmpty
          ? 0
          : ((double.tryParse(cpuLimit.text.trim()) ?? 0) * 1e9).round(),
      memoryLimitBytes: memoryLimit.text.trim().isEmpty
          ? 0
          : (int.tryParse(memoryLimit.text.trim()) ?? 0) * 1024 * 1024,
      memoryReservationBytes: memoryReservation.text.trim().isEmpty
          ? 0
          : (int.tryParse(memoryReservation.text.trim()) ?? 0) * 1024 * 1024,
      healthCheckTest: healthCheckTest.text.trim(),
      healthCheckIntervalSeconds:
          int.tryParse(healthCheckInterval.text.trim()) ?? 0,
      healthCheckTimeoutSeconds:
          int.tryParse(healthCheckTimeout.text.trim()) ?? 0,
      healthCheckRetries: int.tryParse(healthCheckRetries.text.trim()) ?? 0,
      healthCheckStartPeriodSeconds:
          int.tryParse(healthCheckStartPeriod.text.trim()) ?? 0,
    );
  }

  void dispose() {
    name.dispose();
    image.dispose();
    command.dispose();
    for (final p in ports) {
      p.dispose();
    }
    for (final v in volumes) {
      v.dispose();
    }
    for (final e in env) {
      e.dispose();
    }
    cpuLimit.dispose();
    memoryLimit.dispose();
    memoryReservation.dispose();
    healthCheckTest.dispose();
    healthCheckInterval.dispose();
    healthCheckTimeout.dispose();
    healthCheckRetries.dispose();
    healthCheckStartPeriod.dispose();
  }
}

/// Form-based editor for a Compose file's services — the visual
/// counterpart to editing raw YAML directly. Limited to the fields
/// compose.ServiceDraft models (image, command, ports, environment,
/// volumes, restart); anything else in a file falls outside what this
/// editor can represent (see ComposeParseResult.visualEditable).
class ComposeVisualEditor extends StatefulWidget {
  final List<ComposeServiceDraft> initialServices;
  final ValueChanged<List<ComposeServiceDraft>> onChanged;

  const ComposeVisualEditor({
    super.key,
    required this.initialServices,
    required this.onChanged,
  });

  @override
  State<ComposeVisualEditor> createState() => ComposeVisualEditorState();
}

class ComposeVisualEditorState extends State<ComposeVisualEditor> {
  late List<_ServiceState> _services;

  @override
  void initState() {
    super.initState();
    _services = [
      for (final s in widget.initialServices) _ServiceState(s),
    ];
    if (_services.isEmpty) {
      _services.add(_ServiceState(const ComposeServiceDraft(name: 'web')));
    }
  }

  @override
  void dispose() {
    for (final s in _services) {
      s.dispose();
    }
    super.dispose();
  }

  /// Current form state as service drafts — read by the parent before
  /// switching to YAML mode or saving.
  List<ComposeServiceDraft> currentServices() =>
      [for (final s in _services) s.toDraft()];

  void _notify() => widget.onChanged(currentServices());

  void _addService() {
    setState(
      () => _services.add(_ServiceState(const ComposeServiceDraft(name: ''))),
    );
    _notify();
  }

  void _removeService(int index) {
    setState(() => _services.removeAt(index).dispose());
    _notify();
  }

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(12),
      children: [
        for (var i = 0; i < _services.length; i++)
          _ServiceCard(
            key: ValueKey(_services[i]),
            state: _services[i],
            onChanged: _notify,
            onRemove: _services.length > 1 ? () => _removeService(i) : null,
          ),
        const SizedBox(height: 8),
        OutlinedButton.icon(
          onPressed: _addService,
          icon: const Icon(Icons.add),
          label: const Text('Add service'),
        ),
        const SizedBox(height: 24),
      ],
    );
  }
}

class _ServiceCard extends StatelessWidget {
  final _ServiceState state;
  final VoidCallback onChanged;
  final VoidCallback? onRemove;

  const _ServiceCard({
    super.key,
    required this.state,
    required this.onChanged,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.only(bottom: 16),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: state.name,
                    onChanged: (_) => onChanged(),
                    decoration: const InputDecoration(
                      labelText: 'Service name',
                      hintText: 'e.g. web',
                      isDense: true,
                    ),
                  ),
                ),
                if (onRemove != null)
                  IconButton(
                    icon: const Icon(Icons.delete_outline),
                    tooltip: 'Remove service',
                    onPressed: onRemove,
                  ),
              ],
            ),
            const SizedBox(height: 12),
            TextField(
              controller: state.image,
              onChanged: (_) => onChanged(),
              decoration: const InputDecoration(
                labelText: 'Image',
                hintText: 'e.g. nginx:latest',
                isDense: true,
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: state.command,
              onChanged: (_) => onChanged(),
              decoration: const InputDecoration(
                labelText: 'Command (optional)',
                isDense: true,
              ),
            ),
            const SizedBox(height: 16),
            _ListSection(
              label: 'Ports',
              hint: 'host:container, e.g. 8080:80',
              rows: state.ports,
              onChanged: onChanged,
            ),
            _ListSection(
              label: 'Volumes',
              hint: 'name:/path or ./host:/path',
              rows: state.volumes,
              onChanged: onChanged,
            ),
            _EnvSection(rows: state.env, onChanged: onChanged),
            const SizedBox(height: 8),
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  'Restart policy',
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
                DropdownButton<String>(
                  value: state.restart,
                  items: [
                    for (final p in _restartPolicies)
                      DropdownMenuItem(value: p, child: Text(p)),
                  ],
                  onChanged: (v) {
                    if (v == null) return;
                    state.restart = v;
                    onChanged();
                  },
                ),
              ],
            ),
            const SizedBox(height: 16),
            Text('Resource limits', style: Theme.of(context).textTheme.bodyMedium),
            const SizedBox(height: 4),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: state.cpuLimit,
                    onChanged: (_) => onChanged(),
                    keyboardType: const TextInputType.numberWithOptions(
                      decimal: true,
                    ),
                    decoration: const InputDecoration(
                      labelText: 'CPU, in cores',
                      hintText: 'e.g. 1.5',
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    controller: state.memoryLimit,
                    onChanged: (_) => onChanged(),
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: 'Memory limit, MB',
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    controller: state.memoryReservation,
                    onChanged: (_) => onChanged(),
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: 'Memory reservation, MB',
                      isDense: true,
                    ),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 16),
            Text('Health check', style: Theme.of(context).textTheme.bodyMedium),
            const SizedBox(height: 4),
            TextField(
              controller: state.healthCheckTest,
              onChanged: (_) => onChanged(),
              decoration: const InputDecoration(
                labelText: 'Test command (optional)',
                hintText: 'e.g. curl -f http://localhost || exit 1',
                isDense: true,
              ),
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: state.healthCheckInterval,
                    onChanged: (_) => onChanged(),
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: 'Interval, sec',
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    controller: state.healthCheckTimeout,
                    onChanged: (_) => onChanged(),
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: 'Timeout, sec',
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    controller: state.healthCheckRetries,
                    onChanged: (_) => onChanged(),
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: 'Retries',
                      isDense: true,
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: TextField(
                    controller: state.healthCheckStartPeriod,
                    onChanged: (_) => onChanged(),
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: 'Start period, sec',
                      isDense: true,
                    ),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _ListSection extends StatefulWidget {
  final String label;
  final String hint;
  final List<_ListRow> rows;
  final VoidCallback onChanged;

  const _ListSection({
    required this.label,
    required this.hint,
    required this.rows,
    required this.onChanged,
  });

  @override
  State<_ListSection> createState() => _ListSectionState();
}

class _ListSectionState extends State<_ListSection> {
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(widget.label, style: Theme.of(context).textTheme.bodyMedium),
              IconButton(
                icon: const Icon(Icons.add_circle_outline),
                tooltip: 'Add',
                visualDensity: VisualDensity.compact,
                onPressed: () {
                  setState(() => widget.rows.add(_ListRow()));
                  widget.onChanged();
                },
              ),
            ],
          ),
          for (var i = 0; i < widget.rows.length; i++)
            Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: widget.rows[i].text,
                      onChanged: (_) => widget.onChanged(),
                      decoration: InputDecoration(
                        hintText: widget.hint,
                        isDense: true,
                      ),
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.remove_circle_outline),
                    visualDensity: VisualDensity.compact,
                    onPressed: () {
                      setState(() => widget.rows.removeAt(i).dispose());
                      widget.onChanged();
                    },
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

class _EnvSection extends StatefulWidget {
  final List<_EnvRow> rows;
  final VoidCallback onChanged;

  const _EnvSection({required this.rows, required this.onChanged});

  @override
  State<_EnvSection> createState() => _EnvSectionState();
}

class _EnvSectionState extends State<_EnvSection> {
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                'Environment variables',
                style: Theme.of(context).textTheme.bodyMedium,
              ),
              IconButton(
                icon: const Icon(Icons.add_circle_outline),
                tooltip: 'Add',
                visualDensity: VisualDensity.compact,
                onPressed: () {
                  setState(() => widget.rows.add(_EnvRow()));
                  widget.onChanged();
                },
              ),
            ],
          ),
          for (var i = 0; i < widget.rows.length; i++)
            Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: widget.rows[i].key,
                      onChanged: (_) => widget.onChanged(),
                      decoration: const InputDecoration(
                        hintText: 'KEY',
                        isDense: true,
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: TextField(
                      controller: widget.rows[i].value,
                      onChanged: (_) => widget.onChanged(),
                      decoration: const InputDecoration(
                        hintText: 'value',
                        isDense: true,
                      ),
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.remove_circle_outline),
                    visualDensity: VisualDensity.compact,
                    onPressed: () {
                      setState(() => widget.rows.removeAt(i).dispose());
                      widget.onChanged();
                    },
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}
