import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/container.dart';
import '../../theme/app_theme.dart';

const _restartPolicyLabels = {
  'no': 'No',
  'on-failure': 'On failure',
  'always': 'Always',
  'unless-stopped': 'Unless stopped',
};

/// The image/name/command/env/ports/volumes/restart-policy form shared by
/// the create-container and recreate-container dialogs — recreate is the
/// same shape as create, just pre-filled from the container being
/// replaced. Renders its own submit button; the caller supplies [onSubmit]
/// to actually make the API call (each caller's error handling / loading
/// state / success behavior differs enough not to share that part).
class ContainerConfigForm extends StatefulWidget {
  final String title;
  final String submitLabel;
  final ContainerConfig? initial;
  final bool loading;
  final String? error;
  final void Function(ContainerConfig config) onSubmit;

  /// Rendered between the title and the image field — e.g. a server
  /// picker, which only the create dialog needs (recreate/clone already
  /// know their server).
  final Widget? header;

  /// apiClient/serverId enable the port fields' inline conflict check
  /// (GET /api/servers/{id}/ports/check on blur) — optional since the
  /// create dialog may not have a server selected yet, in which case the
  /// check is silently skipped. excludeContainerId suppresses a false
  /// warning for recreate, where the container being replaced still
  /// currently holds its own host ports until the recreate actually runs.
  final ApiClient? apiClient;
  final String? serverId;
  final String? excludeContainerId;

  const ContainerConfigForm({
    super.key,
    required this.title,
    required this.submitLabel,
    this.initial,
    required this.loading,
    this.error,
    required this.onSubmit,
    this.header,
    this.apiClient,
    this.serverId,
    this.excludeContainerId,
  });

  @override
  State<ContainerConfigForm> createState() => _ContainerConfigFormState();
}

class _KeyValueRow {
  final TextEditingController key;
  final TextEditingController value;
  _KeyValueRow({String key = '', String value = ''})
    : key = TextEditingController(text: key),
      value = TextEditingController(text: value);
  void dispose() {
    key.dispose();
    value.dispose();
  }
}

class _PortRow {
  final TextEditingController containerPort;
  final TextEditingController hostPort;
  String protocol;
  _PortRow({
    String containerPort = '',
    String hostPort = '',
    this.protocol = 'tcp',
  }) : containerPort = TextEditingController(text: containerPort),
       hostPort = TextEditingController(text: hostPort);
  void dispose() {
    containerPort.dispose();
    hostPort.dispose();
  }
}

class _VolumeRow {
  final TextEditingController name;
  final TextEditingController target;
  bool readOnly;
  _VolumeRow({String name = '', String target = '', this.readOnly = false})
    : name = TextEditingController(text: name),
      target = TextEditingController(text: target);
  void dispose() {
    name.dispose();
    target.dispose();
  }
}

class _ContainerConfigFormState extends State<ContainerConfigForm> {
  final _formKey = GlobalKey<FormState>();
  late final _imageController = TextEditingController(
    text: widget.initial?.image ?? '',
  );
  late final _nameController = TextEditingController(
    text: widget.initial?.name ?? '',
  );
  late final _commandController = TextEditingController(
    text: (widget.initial?.command ?? const []).join(' '),
  );
  late final List<_KeyValueRow> _env = [
    for (final e in widget.initial?.env ?? const [])
      _KeyValueRow(
        key: e.contains('=') ? e.substring(0, e.indexOf('=')) : e,
        value: e.contains('=') ? e.substring(e.indexOf('=') + 1) : '',
      ),
  ];
  late final List<_PortRow> _ports = [
    for (final p in widget.initial?.ports ?? const [])
      _PortRow(
        containerPort: '${p.containerPort}',
        hostPort: p.hostPort == 0 ? '' : '${p.hostPort}',
        protocol: p.protocol.isEmpty ? 'tcp' : p.protocol,
      ),
  ];
  late final List<_VolumeRow> _volumes = [
    for (final v in widget.initial?.volumes ?? const [])
      _VolumeRow(name: v.volumeName, target: v.target, readOnly: v.readOnly),
  ];
  late String _restartPolicy = widget.initial?.restartPolicyName ?? 'no';
  late final _maxRetryController = TextEditingController(
    text:
        widget.initial == null ||
            widget.initial!.restartPolicyMaxRetryCount == 0
        ? ''
        : '${widget.initial!.restartPolicyMaxRetryCount}',
  );
  late final _cpuController = TextEditingController(
    text: widget.initial == null || widget.initial!.nanoCpus == 0
        ? ''
        : (widget.initial!.nanoCpus / 1000000000).toStringAsFixed(2),
  );
  late final _memLimitController = TextEditingController(
    text: widget.initial == null || widget.initial!.memoryLimitBytes == 0
        ? ''
        : '${widget.initial!.memoryLimitBytes ~/ (1024 * 1024)}',
  );
  late final _memReservationController = TextEditingController(
    text: widget.initial == null || widget.initial!.memoryReservationBytes == 0
        ? ''
        : '${widget.initial!.memoryReservationBytes ~/ (1024 * 1024)}',
  );
  late final _pidsController = TextEditingController(
    text: widget.initial == null || widget.initial!.pidsLimit == 0
        ? ''
        : '${widget.initial!.pidsLimit}',
  );

  @override
  void dispose() {
    _imageController.dispose();
    _nameController.dispose();
    _commandController.dispose();
    for (final e in _env) {
      e.dispose();
    }
    for (final p in _ports) {
      p.dispose();
    }
    for (final v in _volumes) {
      v.dispose();
    }
    _maxRetryController.dispose();
    _cpuController.dispose();
    _memLimitController.dispose();
    _memReservationController.dispose();
    _pidsController.dispose();
    super.dispose();
  }

  void _submit() {
    if (!_formKey.currentState!.validate()) return;

    final config = ContainerConfig(
      image: _imageController.text.trim(),
      name: _nameController.text.trim(),
      command: _commandController.text.trim().isEmpty
          ? const []
          : _commandController.text.trim().split(RegExp(r'\s+')),
      env: [
        for (final e in _env)
          if (e.key.text.trim().isNotEmpty)
            '${e.key.text.trim()}=${e.value.text}',
      ],
      ports: [
        for (final p in _ports)
          if (p.containerPort.text.trim().isNotEmpty)
            ContainerPortSpec(
              containerPort: int.parse(p.containerPort.text.trim()),
              hostPort: p.hostPort.text.trim().isEmpty
                  ? 0
                  : int.parse(p.hostPort.text.trim()),
              protocol: p.protocol,
            ),
      ],
      volumes: [
        for (final v in _volumes)
          if (v.name.text.trim().isNotEmpty && v.target.text.trim().isNotEmpty)
            ContainerVolumeSpec(
              volumeName: v.name.text.trim(),
              target: v.target.text.trim(),
              readOnly: v.readOnly,
            ),
      ],
      restartPolicyName: _restartPolicy,
      restartPolicyMaxRetryCount:
          _restartPolicy == 'on-failure' &&
              _maxRetryController.text.trim().isNotEmpty
          ? int.parse(_maxRetryController.text.trim())
          : 0,
      nanoCpus: _cpuController.text.trim().isEmpty
          ? 0
          : ((double.tryParse(_cpuController.text.trim()) ?? 0) * 1000000000)
                .round(),
      memoryLimitBytes: _memLimitController.text.trim().isEmpty
          ? 0
          : int.parse(_memLimitController.text.trim()) * 1024 * 1024,
      memoryReservationBytes: _memReservationController.text.trim().isEmpty
          ? 0
          : int.parse(_memReservationController.text.trim()) * 1024 * 1024,
      pidsLimit: _pidsController.text.trim().isEmpty
          ? 0
          : int.parse(_pidsController.text.trim()),
    );
    widget.onSubmit(config);
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(
        left: 20,
        right: 20,
        top: 20,
        bottom: MediaQuery.of(context).viewInsets.bottom + 20,
      ),
      child: Form(
        key: _formKey,
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(widget.title, style: Theme.of(context).textTheme.titleLarge),
              if (widget.header != null) ...[
                const SizedBox(height: 12),
                widget.header!,
              ],
              const SizedBox(height: 16),
              TextFormField(
                controller: _imageController,
                decoration: const InputDecoration(
                  labelText: 'Image',
                  hintText: 'e.g. nginx:latest',
                ),
                validator: (v) =>
                    (v == null || v.trim().isEmpty) ? 'Required' : null,
              ),
              const SizedBox(height: 12),
              TextFormField(
                controller: _nameController,
                decoration: const InputDecoration(labelText: 'Container name'),
                validator: (v) =>
                    (v == null || v.trim().isEmpty) ? 'Required' : null,
              ),
              const SizedBox(height: 12),
              TextFormField(
                controller: _commandController,
                decoration: const InputDecoration(
                  labelText: 'Command (optional)',
                  hintText: 'space-separated, e.g. python app.py',
                ),
              ),
              const SizedBox(height: 16),
              _SectionHeader(
                label: 'Environment variables',
                onAdd: () => setState(() => _env.add(_KeyValueRow())),
              ),
              for (var i = 0; i < _env.length; i++)
                _KeyValueField(
                  row: _env[i],
                  keyHint: 'KEY',
                  valueHint: 'value',
                  onRemove: () => setState(() {
                    _env.removeAt(i).dispose();
                  }),
                ),
              const SizedBox(height: 16),
              _SectionHeader(
                label: 'Ports',
                onAdd: () => setState(() => _ports.add(_PortRow())),
              ),
              for (var i = 0; i < _ports.length; i++)
                _PortField(
                  key: ValueKey(_ports[i]),
                  row: _ports[i],
                  apiClient: widget.apiClient,
                  serverId: widget.serverId,
                  excludeContainerId: widget.excludeContainerId,
                  onProtocolChanged: (v) =>
                      setState(() => _ports[i].protocol = v),
                  onRemove: () => setState(() {
                    _ports.removeAt(i).dispose();
                  }),
                ),
              const SizedBox(height: 16),
              _SectionHeader(
                label: 'Named volumes',
                onAdd: () => setState(() => _volumes.add(_VolumeRow())),
              ),
              for (var i = 0; i < _volumes.length; i++)
                _VolumeField(
                  row: _volumes[i],
                  onReadOnlyChanged: (v) =>
                      setState(() => _volumes[i].readOnly = v),
                  onRemove: () => setState(() {
                    _volumes.removeAt(i).dispose();
                  }),
                ),
              const SizedBox(height: 16),
              Text(
                'Restart policy',
                style: Theme.of(context).textTheme.titleSmall,
              ),
              const SizedBox(height: 4),
              DropdownButtonFormField<String>(
                initialValue: _restartPolicy,
                items: [
                  for (final entry in _restartPolicyLabels.entries)
                    DropdownMenuItem(
                      value: entry.key,
                      child: Text(entry.value),
                    ),
                ],
                onChanged: (v) => setState(() => _restartPolicy = v ?? 'no'),
              ),
              if (_restartPolicy == 'on-failure') ...[
                const SizedBox(height: 8),
                TextFormField(
                  controller: _maxRetryController,
                  keyboardType: TextInputType.number,
                  decoration: const InputDecoration(
                    labelText: 'Max retry count (0 = unlimited)',
                  ),
                ),
              ],
              const SizedBox(height: 16),
              Text(
                'Resource limits',
                style: Theme.of(context).textTheme.titleSmall,
              ),
              const SizedBox(height: 4),
              TextFormField(
                controller: _cpuController,
                keyboardType: const TextInputType.numberWithOptions(
                  decimal: true,
                ),
                decoration: const InputDecoration(
                  labelText: 'CPU limit, in cores (blank = unlimited)',
                  hintText: 'e.g. 1.5',
                ),
              ),
              const SizedBox(height: 8),
              TextFormField(
                controller: _memLimitController,
                keyboardType: TextInputType.number,
                decoration: const InputDecoration(
                  labelText: 'Memory limit, in MB (blank = unlimited)',
                ),
              ),
              const SizedBox(height: 8),
              TextFormField(
                controller: _memReservationController,
                keyboardType: TextInputType.number,
                decoration: const InputDecoration(
                  labelText: 'Memory reservation, in MB (blank = none)',
                ),
              ),
              const SizedBox(height: 8),
              TextFormField(
                controller: _pidsController,
                keyboardType: TextInputType.number,
                decoration: const InputDecoration(
                  labelText: 'Process limit (blank = unlimited)',
                ),
              ),
              if (widget.error != null) ...[
                const SizedBox(height: 12),
                Text(widget.error!, style: const TextStyle(color: AppColors.failed)),
              ],
              const SizedBox(height: 20),
              FilledButton(
                onPressed: widget.loading ? null : _submit,
                child: widget.loading
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : Text(widget.submitLabel),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String label;
  final VoidCallback onAdd;

  const _SectionHeader({required this.label, required this.onAdd});

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        Text(label, style: Theme.of(context).textTheme.titleSmall),
        IconButton(
          icon: const Icon(Icons.add_circle_outline),
          tooltip: 'Add',
          onPressed: onAdd,
          visualDensity: VisualDensity.compact,
        ),
      ],
    );
  }
}

class _KeyValueField extends StatelessWidget {
  final _KeyValueRow row;
  final String keyHint;
  final String valueHint;
  final VoidCallback onRemove;

  const _KeyValueField({
    required this.row,
    required this.keyHint,
    required this.valueHint,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        children: [
          Expanded(
            child: TextFormField(
              controller: row.key,
              decoration: InputDecoration(hintText: keyHint, isDense: true),
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: TextFormField(
              controller: row.value,
              decoration: InputDecoration(hintText: valueHint, isDense: true),
            ),
          ),
          IconButton(
            icon: const Icon(Icons.remove_circle_outline, size: 20),
            onPressed: onRemove,
            visualDensity: VisualDensity.compact,
          ),
        ],
      ),
    );
  }
}

class _PortField extends StatefulWidget {
  final _PortRow row;
  final ApiClient? apiClient;
  final String? serverId;
  final String? excludeContainerId;
  final ValueChanged<String> onProtocolChanged;
  final VoidCallback onRemove;

  const _PortField({
    super.key,
    required this.row,
    this.apiClient,
    this.serverId,
    this.excludeContainerId,
    required this.onProtocolChanged,
    required this.onRemove,
  });

  @override
  State<_PortField> createState() => _PortFieldState();
}

class _PortFieldState extends State<_PortField> {
  final _hostPortFocus = FocusNode();
  String? _conflictWarning;

  @override
  void initState() {
    super.initState();
    _hostPortFocus.addListener(_onFocusChange);
  }

  @override
  void dispose() {
    _hostPortFocus.removeListener(_onFocusChange);
    _hostPortFocus.dispose();
    super.dispose();
  }

  void _onFocusChange() {
    if (!_hostPortFocus.hasFocus) _checkConflict();
  }

  Future<void> _checkConflict() async {
    final apiClient = widget.apiClient;
    final serverId = widget.serverId;
    final hostPort = int.tryParse(widget.row.hostPort.text.trim());
    if (apiClient == null || serverId == null || hostPort == null) {
      if (mounted && _conflictWarning != null) {
        setState(() => _conflictWarning = null);
      }
      return;
    }
    try {
      final result = await apiClient.checkPortConflict(
        serverId,
        hostPort: hostPort,
        protocol: widget.row.protocol,
      );
      if (!mounted) return;
      final conflicted =
          result.conflict && result.containerId != widget.excludeContainerId;
      setState(() {
        _conflictWarning = conflicted
            ? 'Port $hostPort/${widget.row.protocol} is already in use'
            : null;
      });
    } catch (_) {
      // Best-effort UI hint — a failed check just means no warning shown;
      // the server-side guard on create/recreate still catches it.
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: TextFormField(
                  controller: widget.row.containerPort,
                  keyboardType: TextInputType.number,
                  decoration: const InputDecoration(
                    hintText: 'Container port',
                    isDense: true,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: TextFormField(
                  controller: widget.row.hostPort,
                  focusNode: _hostPortFocus,
                  keyboardType: TextInputType.number,
                  decoration: const InputDecoration(
                    hintText: 'Host port (optional)',
                    isDense: true,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              DropdownButton<String>(
                value: widget.row.protocol,
                items: const [
                  DropdownMenuItem(value: 'tcp', child: Text('TCP')),
                  DropdownMenuItem(value: 'udp', child: Text('UDP')),
                ],
                onChanged: (v) => widget.onProtocolChanged(v ?? 'tcp'),
              ),
              IconButton(
                icon: const Icon(Icons.remove_circle_outline, size: 20),
                onPressed: widget.onRemove,
                visualDensity: VisualDensity.compact,
              ),
            ],
          ),
          if (_conflictWarning != null)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Row(
                children: [
                  const Icon(
                    Icons.warning_amber,
                    size: 14,
                    color: AppColors.warning,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    _conflictWarning!,
                    style: const TextStyle(color: AppColors.warning, fontSize: 12),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

class _VolumeField extends StatelessWidget {
  final _VolumeRow row;
  final ValueChanged<bool> onReadOnlyChanged;
  final VoidCallback onRemove;

  const _VolumeField({
    required this.row,
    required this.onReadOnlyChanged,
    required this.onRemove,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        children: [
          Expanded(
            child: TextFormField(
              controller: row.name,
              decoration: const InputDecoration(
                hintText: 'Volume name',
                isDense: true,
              ),
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: TextFormField(
              controller: row.target,
              decoration: const InputDecoration(
                hintText: 'Mount path',
                isDense: true,
              ),
            ),
          ),
          Checkbox(
            value: row.readOnly,
            onChanged: (v) => onReadOnlyChanged(v ?? false),
          ),
          const Text('RO', style: TextStyle(fontSize: 12)),
          IconButton(
            icon: const Icon(Icons.remove_circle_outline, size: 20),
            onPressed: onRemove,
            visualDensity: VisualDensity.compact,
          ),
        ],
      ),
    );
  }
}
