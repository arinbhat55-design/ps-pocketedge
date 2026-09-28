import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../models/database.dart';
import '../../models/server.dart';
import '../../theme/app_theme.dart';
import '../../widgets/notice_banner.dart';
import '../../widgets/status_pill.dart';
import 'database_widgets.dart';

/// The one-click deployment wizard for one catalog engine: basics,
/// resources and access, backups, then a server-validated review of
/// exactly what will be deployed. Pops with the created instance.
class DatabaseWizardScreen extends StatefulWidget {
  final ApiClient apiClient;
  final DatabaseEngine engine;
  final DatabaseInstance? cloneSource;
  final String? cloneBackupId;

  const DatabaseWizardScreen({
    super.key,
    required this.apiClient,
    required this.engine,
    this.cloneSource,
    this.cloneBackupId,
  });

  @override
  State<DatabaseWizardScreen> createState() => _DatabaseWizardScreenState();
}

class _DatabaseWizardScreenState extends State<DatabaseWizardScreen> {
  static final _namePattern = RegExp(r'^[a-z][a-z0-9-]{1,38}[a-z0-9]$');
  static final _identifierPattern = RegExp(r'^[A-Za-z_][A-Za-z0-9_]{0,62}$');

  DatabaseEngine get engine => widget.engine;

  int _step = 0;
  final _basicsKey = GlobalKey<FormState>();
  final _resourcesKey = GlobalKey<FormState>();
  final _backupKey = GlobalKey<FormState>();

  late String _version = engine.versions.first.tag;
  final _name = TextEditingController();
  final _databaseName = TextEditingController(text: 'app');
  late final _username = TextEditingController(
    text: engine.defaultUsername ?? '',
  );
  String? _serverId;
  String _profile = 'development';
  late String? _edition = engine.editions.isEmpty
      ? null
      : engine.editions.first;
  bool _acceptLicense = false;

  late final _port = TextEditingController(text: '${engine.port.container}');
  String _access = 'local';
  late final _memory = TextEditingController(text: '${engine.defaultMemoryMb}');
  late final _cpus = TextEditingController(text: _fmtCpus(engine.defaultCpus));
  late final _storage = TextEditingController(
    text: '${engine.defaultStorageGb}',
  );
  bool _highAvailability = false;

  String _backupCron = '';
  bool _consistentBackups = true;
  final _retentionDays = TextEditingController(text: '0');
  final _retentionCount = TextEditingController(text: '0');
  final _changeRequest = TextEditingController();

  late Future<List<Server>> _serversFuture;
  Future<DatabasePreview>? _previewFuture;
  bool _deploying = false;

  static String _fmtCpus(double v) =>
      v == v.roundToDouble() ? v.toInt().toString() : v.toString();

  @override
  void initState() {
    super.initState();
    final source = widget.cloneSource;
    if (source != null) {
      _version = engine.versions.any((v) => v.tag == source.version)
          ? source.version
          : source.version.split('.').first;
      _name.text = '${source.name}-clone';
      _databaseName.text = source.databaseName;
      _username.text = source.adminUsername;
      _serverId = source.serverId;
      _profile = source.profile;
      _port.text = '${source.port + 1}';
      _access = source.access;
      _memory.text = '${source.memoryMb}';
      _cpus.text = _fmtCpus(source.cpus);
      _storage.text = '${source.storageGb}';
      _highAvailability = source.highAvailability;
      _consistentBackups = true;
    }
    _serversFuture = widget.apiClient.listServers();
  }

  @override
  void dispose() {
    for (final c in [
      _name,
      _databaseName,
      _username,
      _port,
      _memory,
      _cpus,
      _storage,
      _retentionDays,
      _retentionCount,
      _changeRequest,
    ]) {
      c.dispose();
    }
    super.dispose();
  }

  /// Production defaults: daily backups kept for a week, no Developer
  /// edition.
  void _setProfile(String profile) {
    setState(() {
      _profile = profile;
      if (profile == 'production') {
        if (engine.persistent && _backupCron.isEmpty) {
          _backupCron = '0 2 * * *';
          _retentionDays.text = '7';
          _retentionCount.text = '14';
        }
        if (_edition == 'Developer') {
          _edition = engine.editions.firstWhere(
            (e) => e != 'Developer',
            orElse: () => _edition!,
          );
        }
      }
    });
  }

  DatabaseRequest _request() => DatabaseRequest(
    engine: engine.id,
    version: _version,
    name: _name.text.trim(),
    serverId: _serverId ?? '',
    databaseName: engine.databaseNameAllowed ? _databaseName.text.trim() : '',
    username: engine.fixedUsername ?? _username.text.trim(),
    port: int.tryParse(_port.text) ?? engine.port.container,
    access: _access,
    storageGb: engine.persistent ? (int.tryParse(_storage.text) ?? 0) : 0,
    memoryMb: int.tryParse(_memory.text) ?? engine.defaultMemoryMb,
    cpus: double.tryParse(_cpus.text) ?? engine.defaultCpus,
    highAvailability: _highAvailability,
    profile: _profile,
    edition: _edition,
    acceptLicense: _acceptLicense,
    backupCron: _backupCron,
    consistentBackups: _consistentBackups,
    retentionDays: int.tryParse(_retentionDays.text) ?? 0,
    retentionCount: int.tryParse(_retentionCount.text) ?? 0,
    changeRequest: _changeRequest.text.trim(),
    cloneBackupId: widget.cloneBackupId,
  );

  int get _lastStep => engine.persistent ? 3 : 2;
  int get _reviewStep => _lastStep;

  void _continue() {
    final keys = [_basicsKey, _resourcesKey, if (engine.persistent) _backupKey];
    if (_step < keys.length &&
        !(keys[_step].currentState?.validate() ?? true)) {
      return;
    }
    if (_step == 0 && engine.requiresLicenseAcceptance && !_acceptLicense) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Accept the license terms to continue.')),
      );
      return;
    }
    if (_step < _lastStep) {
      setState(() {
        _step++;
        if (_step == _reviewStep) {
          _previewFuture = widget.apiClient.previewDatabase(_request());
        }
      });
    }
  }

  Future<void> _deploy() async {
    setState(() => _deploying = true);
    try {
      final result = await widget.apiClient.createDatabase(_request());
      if (!mounted) return;
      final status = result.deploymentStatus;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(switch (status) {
            'pending_approval' =>
              '${result.database.name} is waiting for deployment approval.',
            'scheduled' =>
              '${result.database.name} will deploy in the next maintenance window.',
            _ => 'Deploying ${result.database.name}…',
          }),
        ),
      );
      Navigator.of(context).pop(result.database);
    } catch (e) {
      if (!mounted) return;
      setState(() => _deploying = false);
      showDialog<void>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Deployment not started'),
          content: Text(e is ApiException ? e.message : '$e'),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('OK'),
            ),
          ],
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final steps = <Step>[
      Step(
        title: const Text('Basics'),
        subtitle: const Text('Version, name, server and administrator'),
        isActive: _step >= 0,
        state: _step > 0 ? StepState.complete : StepState.indexed,
        content: Form(key: _basicsKey, child: _buildBasics(context)),
      ),
      Step(
        title: const Text('Resources & access'),
        subtitle: const Text('Port, memory, CPU, storage, availability'),
        isActive: _step >= 1,
        state: _step > 1 ? StepState.complete : StepState.indexed,
        content: Form(key: _resourcesKey, child: _buildResources(context)),
      ),
      if (engine.persistent)
        Step(
          title: const Text('Backups'),
          subtitle: const Text('Schedule and retention'),
          isActive: _step >= 2,
          state: _step > 2 ? StepState.complete : StepState.indexed,
          content: Form(key: _backupKey, child: _buildBackups(context)),
        ),
      Step(
        title: const Text('Review & deploy'),
        isActive: _step >= _reviewStep,
        content: _buildReview(context),
      ),
    ];

    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.cloneSource == null
              ? 'Deploy ${engine.name}'
              : 'Create clone of ${widget.cloneSource!.name}',
        ),
      ),
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 860),
          child: Stepper(
            currentStep: _step,
            onStepTapped: (i) {
              if (i < _step) setState(() => _step = i);
            },
            onStepContinue: _step == _reviewStep ? null : _continue,
            onStepCancel: _step == 0 ? null : () => setState(() => _step--),
            controlsBuilder: (context, details) {
              if (_step == _reviewStep) {
                return Padding(
                  padding: const EdgeInsets.only(top: Space.lg),
                  child: Wrap(
                    spacing: Space.sm,
                    runSpacing: Space.sm,
                    children: [
                      FutureBuilder<DatabasePreview>(
                        future: _previewFuture,
                        builder: (context, snap) => FilledButton.icon(
                          onPressed: snap.hasData && !_deploying
                              ? _deploy
                              : null,
                          icon: _deploying
                              ? const SizedBox(
                                  width: 16,
                                  height: 16,
                                  child: CircularProgressIndicator(
                                    strokeWidth: 2,
                                  ),
                                )
                              : const Icon(Icons.rocket_launch, size: 18),
                          label: const Text('Deploy database'),
                        ),
                      ),
                      TextButton(
                        onPressed: _deploying ? null : details.onStepCancel,
                        child: const Text('Back'),
                      ),
                    ],
                  ),
                );
              }
              return Padding(
                padding: const EdgeInsets.only(top: Space.lg),
                child: Row(
                  children: [
                    FilledButton(
                      onPressed: details.onStepContinue,
                      child: const Text('Continue'),
                    ),
                    const SizedBox(width: Space.sm),
                    if (_step > 0)
                      TextButton(
                        onPressed: details.onStepCancel,
                        child: const Text('Back'),
                      ),
                  ],
                ),
              );
            },
            steps: steps,
          ),
        ),
      ),
    );
  }

  Widget _buildBasics(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        DropdownButtonFormField<String>(
          isExpanded: true,
          initialValue: _version,
          decoration: const InputDecoration(labelText: 'Version'),
          items: [
            for (final v in engine.versions)
              DropdownMenuItem(value: v.tag, child: Text(v.label)),
          ],
          onChanged: (v) => setState(() => _version = v ?? _version),
        ),
        const SizedBox(height: Space.md),
        TextFormField(
          controller: _name,
          decoration: const InputDecoration(
            labelText: 'Instance name',
            helperText: 'Lowercase letters, digits and hyphens, e.g. orders-db',
          ),
          inputFormatters: [
            FilteringTextInputFormatter.allow(RegExp('[a-z0-9-]')),
          ],
          validator: (v) => _namePattern.hasMatch(v?.trim() ?? '')
              ? null
              : '3-40 characters, starting with a letter',
        ),
        const SizedBox(height: Space.md),
        FutureBuilder<List<Server>>(
          future: _serversFuture,
          builder: (context, snapshot) {
            final servers = (snapshot.data ?? [])
                .where((s) => s.status != 'removed')
                .toList();
            return DropdownButtonFormField<String>(
              isExpanded: true,
              initialValue: _serverId,
              decoration: InputDecoration(
                labelText: 'Server',
                helperText: snapshot.hasError
                    ? 'Couldn\'t load servers'
                    : 'Images are published for ${engine.architectures.join(', ')}',
              ),
              items: [
                for (final s in servers)
                  DropdownMenuItem(
                    value: s.id,
                    enabled:
                        s.arch.isEmpty || engine.architectures.contains(s.arch),
                    child: Text(
                      '${s.name} (${s.arch})'
                      '${s.arch.isNotEmpty && !engine.architectures.contains(s.arch) ? ' — unsupported' : ''}',
                    ),
                  ),
              ],
              onChanged: (v) => setState(() => _serverId = v),
              validator: (v) => v == null ? 'Choose a server' : null,
            );
          },
        ),
        if (engine.databaseNameAllowed) ...[
          const SizedBox(height: Space.md),
          TextFormField(
            controller: _databaseName,
            decoration: InputDecoration(
              labelText: engine.databaseNameLabel ?? 'Database name',
            ),
            validator: (v) => _identifierPattern.hasMatch(v?.trim() ?? '')
                ? null
                : 'Letters, digits and underscores; start with a letter',
          ),
        ],
        if (engine.usernameAllowed) ...[
          const SizedBox(height: Space.md),
          TextFormField(
            key: ValueKey(engine.fixedUsername),
            controller: engine.fixedUsername != null ? null : _username,
            initialValue: engine.fixedUsername,
            enabled: engine.fixedUsername == null,
            decoration: InputDecoration(
              labelText: 'Administrator username',
              helperText: engine.fixedUsername != null
                  ? '${engine.name} always uses "${engine.fixedUsername}"'
                  : null,
            ),
            validator: engine.fixedUsername != null
                ? null
                : (v) => _identifierPattern.hasMatch(v?.trim() ?? '')
                      ? null
                      : 'Letters, digits and underscores; start with a letter',
          ),
        ],
        const SizedBox(height: Space.lg),
        Text('Profile', style: Theme.of(context).textTheme.labelLarge),
        const SizedBox(height: Space.sm),
        SegmentedButton<String>(
          segments: const [
            ButtonSegment(
              value: 'development',
              icon: Icon(Icons.science_outlined),
              label: Text('Development'),
            ),
            ButtonSegment(
              value: 'production',
              icon: Icon(Icons.verified_outlined),
              label: Text('Production'),
            ),
          ],
          selected: {_profile},
          onSelectionChanged: (s) => _setProfile(s.first),
        ),
        const SizedBox(height: Space.xs),
        Text(
          _profile == 'production'
              ? 'Always restarts, reserves memory, stricter health checks, '
                    'scheduled backups on by default. Your production '
                    'deployment policy (approvals, maintenance windows) applies.'
              : 'Restarts unless stopped; no backups unless you schedule them.',
          style: Theme.of(context).textTheme.bodySmall,
        ),
        if (engine.editions.isNotEmpty) ...[
          const SizedBox(height: Space.md),
          DropdownButtonFormField<String>(
            isExpanded: true,
            initialValue: _edition,
            decoration: const InputDecoration(labelText: 'Edition'),
            items: [
              for (final e in engine.editions)
                DropdownMenuItem(
                  value: e,
                  enabled: !(e == 'Developer' && _profile == 'production'),
                  child: Text(e),
                ),
            ],
            onChanged: (v) => setState(() => _edition = v),
          ),
        ],
        const SizedBox(height: Space.md),
        Text(
          'License: ${engine.license}',
          style: Theme.of(context).textTheme.bodySmall,
        ),
        if (engine.requiresLicenseAcceptance)
          CheckboxListTile(
            contentPadding: EdgeInsets.zero,
            value: _acceptLicense,
            onChanged: (v) => setState(() => _acceptLicense = v ?? false),
            title: Text('I accept the ${engine.name} license terms'),
            controlAffinity: ListTileControlAffinity.leading,
          ),
        for (final note in engine.notes)
          Padding(
            padding: const EdgeInsets.only(top: Space.sm),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Icon(Icons.info_outline, size: 16),
                const SizedBox(width: Space.sm),
                Expanded(
                  child: Text(
                    note,
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ),
              ],
            ),
          ),
      ],
    );
  }

  String? _intRange(String? v, int min, int max, String what) {
    final n = int.tryParse(v?.trim() ?? '');
    if (n == null || n < min || n > max) return '$what must be $min–$max';
    return null;
  }

  Widget _buildResources(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TextFormField(
          controller: _port,
          keyboardType: TextInputType.number,
          inputFormatters: [FilteringTextInputFormatter.digitsOnly],
          decoration: InputDecoration(
            labelText: 'Host port',
            helperText:
                '${engine.port.name} listens on ${engine.port.container} in the container'
                '${engine.extraPorts.isEmpty ? '' : '; other ports (${engine.extraPorts.map((p) => p.name).join(', ')}) shift by the same offset'}',
          ),
          validator: (v) => _intRange(v, 1024, 65535, 'Port'),
        ),
        const SizedBox(height: Space.lg),
        Text('Access', style: theme.textTheme.labelLarge),
        const SizedBox(height: Space.sm),
        SegmentedButton<String>(
          segments: const [
            ButtonSegment(
              value: 'local',
              icon: Icon(Icons.lock_outline),
              label: Text('Local only'),
            ),
            ButtonSegment(
              value: 'remote',
              icon: Icon(Icons.public),
              label: Text('Remote'),
            ),
          ],
          selected: {_access},
          onSelectionChanged: (s) => setState(() => _access = s.first),
        ),
        const SizedBox(height: Space.xs),
        Text(
          _access == 'local'
              ? 'Bound to 127.0.0.1 on the server: reachable from the server '
                    'itself or through an SSH tunnel.'
              : 'Bound to every interface: reachable from any network that '
                    'can reach the server. Put a firewall in front of it.',
          style: theme.textTheme.bodySmall,
        ),
        if (_access == 'remote' && !engine.hasCredentials) ...[
          const SizedBox(height: Space.sm),
          NoticeBanner(
            tone: StatusTone.failed,
            icon: Icons.warning_amber,
            title: 'No authentication',
            message:
                '${engine.name} runs without authentication here. Remote '
                'access lets anyone who can reach the port read and change '
                'its data.',
          ),
        ],
        const SizedBox(height: Space.lg),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: TextFormField(
                controller: _memory,
                keyboardType: TextInputType.number,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                decoration: InputDecoration(
                  labelText: 'Memory limit (MB)',
                  helperText: 'Minimum ${engine.minMemoryMb} MB',
                ),
                validator: (v) =>
                    _intRange(v, engine.minMemoryMb, 1048576, 'Memory'),
              ),
            ),
            const SizedBox(width: Space.md),
            Expanded(
              child: TextFormField(
                controller: _cpus,
                keyboardType: const TextInputType.numberWithOptions(
                  decimal: true,
                ),
                decoration: const InputDecoration(labelText: 'CPU limit'),
                validator: (v) {
                  final n = double.tryParse(v?.trim() ?? '');
                  return n == null || n < 0.25 || n > 64
                      ? 'CPUs must be 0.25–64'
                      : null;
                },
              ),
            ),
          ],
        ),
        if (engine.persistent) ...[
          const SizedBox(height: Space.md),
          TextFormField(
            controller: _storage,
            keyboardType: TextInputType.number,
            inputFormatters: [FilteringTextInputFormatter.digitsOnly],
            decoration: const InputDecoration(
              labelText: 'Storage allocation (GB)',
              helperText:
                  'Checked against the server\'s free disk. Docker\'s local '
                  'volume driver doesn\'t enforce a hard limit.',
            ),
            validator: (v) => _intRange(v, 1, 65536, 'Storage'),
          ),
        ],
        if (engine.supportsHighAvailability) ...[
          const SizedBox(height: Space.md),
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            value: _highAvailability,
            onChanged: (v) => setState(() => _highAvailability = v),
            title: const Text('High availability'),
            subtitle: Text(engine.highAvailability!),
          ),
        ],
      ],
    );
  }

  Widget _buildBackups(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        DropdownButtonFormField<String>(
          isExpanded: true,
          initialValue: _backupCron,
          decoration: const InputDecoration(labelText: 'Backup schedule'),
          items: [
            for (final p in backupPresets.entries)
              DropdownMenuItem(value: p.key, child: Text(p.value)),
          ],
          onChanged: (v) => setState(() => _backupCron = v ?? ''),
        ),
        const SizedBox(height: Space.md),
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          value: _consistentBackups,
          onChanged: (v) => setState(() => _consistentBackups = v),
          title: const Text('Consistent backups'),
          subtitle: const Text(
            'Stop the database for the few seconds a snapshot takes, so '
            'files are copied at rest. Off: snapshot while running '
            '(no downtime, but may need crash recovery on restore).',
          ),
        ),
        const SizedBox(height: Space.md),
        Text('Retention', style: theme.textTheme.labelLarge),
        const SizedBox(height: Space.sm),
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: TextFormField(
                controller: _retentionDays,
                keyboardType: TextInputType.number,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                decoration: const InputDecoration(
                  labelText: 'Delete after (days)',
                  helperText: '0 = never by age',
                ),
                validator: (v) => _intRange(v, 0, 3650, 'Days'),
              ),
            ),
            const SizedBox(width: Space.md),
            Expanded(
              child: TextFormField(
                controller: _retentionCount,
                keyboardType: TextInputType.number,
                inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                decoration: const InputDecoration(
                  labelText: 'Keep newest',
                  helperText: '0 = no count limit',
                ),
                validator: (v) => _intRange(v, 0, 1000, 'Count'),
              ),
            ),
          ],
        ),
      ],
    );
  }

  Widget _buildReview(BuildContext context) {
    final theme = Theme.of(context);
    if (_previewFuture == null) return const SizedBox.shrink();
    return FutureBuilder<DatabasePreview>(
      future: _previewFuture,
      builder: (context, snapshot) {
        if (snapshot.connectionState != ConnectionState.done) {
          return const Padding(
            padding: EdgeInsets.all(Space.lg),
            child: Center(child: CircularProgressIndicator()),
          );
        }
        if (snapshot.hasError) {
          final e = snapshot.error;
          return NoticeBanner(
            tone: StatusTone.failed,
            icon: Icons.error_outline,
            title: 'This configuration can\'t be deployed',
            message: e is ApiException ? e.message : '$e',
            actionLabel: 'Re-check',
            onAction: () => setState(() {
              _previewFuture = widget.apiClient.previewDatabase(_request());
            }),
          );
        }
        final p = snapshot.data!;
        final r = _request();
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            KeyValueRow(label: 'Engine', value: '${engine.name} $_version'),
            KeyValueRow(label: 'Instance', value: r.name),
            KeyValueRow(
              label: 'Resources',
              value:
                  '${r.memoryMb} MB · ${_fmtCpus(r.cpus)} CPU'
                  '${engine.persistent ? ' · ${r.storageGb} GB' : ''}'
                  '${r.highAvailability ? ' · high availability' : ''}',
            ),
            KeyValueRow(
              label: 'Access',
              value: r.access == 'local'
                  ? 'Local only (127.0.0.1:${r.port})'
                  : 'Remote (0.0.0.0:${r.port})',
            ),
            if (engine.persistent)
              KeyValueRow(
                label: 'Backups',
                value:
                    '${describeBackupCron(r.backupCron)} · '
                    '${describeRetention(r.retentionDays, r.retentionCount)}',
              ),
            for (final port in p.extraPorts)
              KeyValueRow(
                label: port.name,
                value: 'port ${port.host} (container ${port.container})',
              ),
            KeyValueRow(
              label: 'Connect with',
              value: p.connectionString,
              monospace: true,
            ),
            const SizedBox(height: Space.md),
            if (p.credentials.isNotEmpty)
              NoticeBanner(
                tone: StatusTone.healthy,
                icon: Icons.key_outlined,
                title: 'Credentials are generated for you',
                message:
                    '${p.credentials.join(' and ')} — 32 random characters, '
                    'stored encrypted in the vault and never written into '
                    'the Compose file or logs. Reveal or download them '
                    'once from the database page after deploying.',
              )
            else
              NoticeBanner(
                tone: StatusTone.warning,
                icon: Icons.lock_open,
                title: 'No credentials',
                message:
                    '${engine.name} runs without authentication in this '
                    'template.',
              ),
            for (final w in p.warnings) ...[
              const SizedBox(height: Space.sm),
              NoticeBanner(
                tone: StatusTone.warning,
                icon: Icons.info_outline,
                title: 'Note',
                message: w,
              ),
            ],
            if (_profile == 'production') ...[
              const SizedBox(height: Space.md),
              TextField(
                controller: _changeRequest,
                decoration: const InputDecoration(
                  labelText: 'Change request (if your policy requires one)',
                ),
              ),
            ],
            const SizedBox(height: Space.md),
            ExpansionTile(
              tilePadding: EdgeInsets.zero,
              title: Text(
                'Generated Compose file',
                style: theme.textTheme.titleSmall,
              ),
              children: [
                Container(
                  width: double.infinity,
                  padding: const EdgeInsets.all(Space.md),
                  decoration: BoxDecoration(
                    color: theme.colorScheme.surfaceContainerLowest,
                    borderRadius: BorderRadius.circular(Radii.sm),
                  ),
                  child: SelectableText(
                    p.composeYaml,
                    style: const TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 12,
                    ),
                  ),
                ),
              ],
            ),
          ],
        );
      },
    );
  }
}
