import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../models/backup.dart';
import '../../models/database.dart';
import '../../models/deployment.dart' show formatTimestamp;
import '../../models/image.dart' show formatBytes;
import '../../theme/app_theme.dart';
import '../../widgets/notice_banner.dart';
import '../../widgets/state_message.dart';
import '../../widgets/status_pill.dart';
import '../deployments/deployment_status_screen.dart';
import 'database_widgets.dart';
import 'secret_dialogs.dart';

/// One deployed database: status, how to connect, its vaulted
/// credentials, and its backups.
class DatabaseDetailScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String databaseId;
  final bool isAdmin;

  const DatabaseDetailScreen({
    super.key,
    required this.apiClient,
    required this.databaseId,
    required this.isAdmin,
  });

  @override
  State<DatabaseDetailScreen> createState() => _DatabaseDetailScreenState();
}

class _DatabaseDetailScreenState extends State<DatabaseDetailScreen> {
  /// How long a revealed value stays on screen before it's masked again.
  static const _revealFor = Duration(seconds: 30);

  late Future<DatabaseDetail> _detailFuture;
  late Future<List<DatabaseCredential>> _credentialsFuture;
  final Map<String, String> _revealed = {};
  final Map<String, Timer> _hideTimers = {};
  final Set<String> _busy = {};
  Timer? _poll;

  ApiClient get api => widget.apiClient;

  @override
  void initState() {
    super.initState();
    _load();
    _poll = Timer.periodic(kListPollInterval, (_) {
      if (mounted) {
        setState(() {
          _detailFuture = api.getDatabase(widget.databaseId);
        });
      }
    });
  }

  @override
  void dispose() {
    _poll?.cancel();
    for (final t in _hideTimers.values) {
      t.cancel();
    }
    super.dispose();
  }

  void _load() {
    _detailFuture = api.getDatabase(widget.databaseId);
    _credentialsFuture = api.listDatabaseCredentials(widget.databaseId);
  }

  void _refresh() => setState(_load);

  void _snack(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  String _error(Object e) => e is ApiException ? e.message : '$e';

  Future<void> _withBusy(String key, Future<void> Function() action) async {
    setState(() => _busy.add(key));
    try {
      await action();
    } catch (e) {
      _snack(_error(e));
    } finally {
      if (mounted) setState(() => _busy.remove(key));
    }
  }

  Future<String?> _reveal(DatabaseCredential c) async {
    if (_revealed.containsKey(c.id)) return _revealed[c.id];
    final secret = await api.revealSecret(c.id);
    if (!mounted) return null;
    setState(() => _revealed[c.id] = secret.value);
    _hideTimers[c.id]?.cancel();
    _hideTimers[c.id] = Timer(_revealFor, () {
      if (mounted) setState(() => _revealed.remove(c.id));
    });
    return secret.value;
  }

  void _hide(DatabaseCredential c) {
    _hideTimers.remove(c.id)?.cancel();
    setState(() => _revealed.remove(c.id));
  }

  Future<void> _copy(DatabaseCredential c) =>
      _withBusy('copy-${c.id}', () async {
        final value = await _reveal(c);
        if (value == null) return;
        await Clipboard.setData(ClipboardData(text: value));
        _snack('Copied. Clear your clipboard when you\'re done.');
      });

  Future<void> _download(DatabaseCredential c) async {
    final ok = await confirm(
      context,
      title: 'Download credentials?',
      message:
          'The credentials file can be downloaded once per credential '
          'version. Save it somewhere safe, such as a password manager — '
          'you won\'t be able to download it again until the credential is '
          'rotated.',
      confirmLabel: 'Download',
    );
    if (!ok) return;
    await _withBusy('download-${c.id}', () async {
      final text = await api.downloadSecret(c.id);
      if (!mounted) return;
      await showCredentialsFileDialog(context, text);
      _refresh();
    });
  }

  Future<void> _rotate(DatabaseInstance d, DatabaseCredential c) async {
    final restart = d.rotation == 'redeploy';
    final ok = await confirm(
      context,
      title: 'Rotate ${c.name.toLowerCase()}?',
      message: restart
          ? '${d.engineName} reads its password when it starts, so a new one '
                'is generated and the database is redeployed to apply it — a '
                'brief restart. Clients must switch to the new value.'
          : 'A new password is generated and set in the running database '
                'without a restart. Existing connections stay open; new ones '
                'need the new password.',
      confirmLabel: 'Rotate',
      destructive: true,
    );
    if (!ok) return;
    await _withBusy('rotate-${c.id}', () async {
      try {
        final result = await api.rotateSecret(c.id);
        _revealed.remove(c.id);
        _snack(result.message);
      } on UnsavedCredentialException catch (e) {
        // The database took the new password but storing it failed — the
        // server hands it back rather than losing it.
        if (mounted) await showUnsavedPasswordDialog(context, e.newPassword);
      }
      _refresh();
    });
  }

  Future<void> _issueTemporary(DatabaseInstance d) async {
    final ttl = await showTemporaryCredentialDialog(context);
    if (ttl == null) return;
    await _withBusy('temporary', () async {
      final c = await api.createTemporaryCredential(d.id, ttl);
      _snack(
        'Issued ${c.username}, read-only, until ${formatTimestamp(c.expiresAt!)}.',
      );
      _refresh();
    });
  }

  Future<void> _revoke(DatabaseCredential c) async {
    final ok = await confirm(
      context,
      title: 'Revoke ${c.username}?',
      message:
          'The login is dropped from the database now and its open sessions are ended.',
      confirmLabel: 'Revoke',
      destructive: true,
    );
    if (!ok) return;
    await _withBusy('revoke-${c.id}', () async {
      await api.revokeSecret(c.id);
      _snack('${c.username} revoked.');
      _refresh();
    });
  }

  Future<void> _share(DatabaseCredential c) =>
      showShareDialog(context, api: api, credential: c);

  Future<void> _backupNow(DatabaseInstance d) => _withBusy('backup', () async {
    await api.backupDatabase(d.id);
    _snack(
      d.backupConsistent
          ? 'Backup started — the database pauses briefly for a consistent snapshot.'
          : 'Backup started.',
    );
    _refresh();
  });

  Future<void> _editPolicy(DatabaseInstance d) async {
    final updated = await showBackupPolicyDialog(
      context,
      api: api,
      instance: d,
    );
    if (updated == true) _refresh();
  }

  void _openDeployment(DatabaseInstance d) {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => DeploymentStatusScreen(
          apiClient: api,
          deploymentId: d.deploymentId,
          isAdmin: widget.isAdmin,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return FutureBuilder<DatabaseDetail>(
      future: _detailFuture,
      builder: (context, snapshot) {
        final detail = snapshot.data;
        return Scaffold(
          appBar: AppBar(
            title: Text(detail?.instance.name ?? 'Database'),
            actions: [
              if (detail != null)
                TextButton.icon(
                  onPressed: () => _openDeployment(detail.instance),
                  icon: const Icon(Icons.rocket_launch_outlined, size: 18),
                  label: const Text('Deployment'),
                ),
              IconButton(
                tooltip: 'Refresh',
                onPressed: _refresh,
                icon: const Icon(Icons.refresh),
              ),
            ],
          ),
          body: switch (snapshot) {
            _ when detail != null => _buildBody(context, detail),
            AsyncSnapshot(hasError: true) => StateMessage.error(
              what: 'this database',
              error: snapshot.error,
              onRetry: _refresh,
            ),
            _ => const Center(child: CircularProgressIndicator()),
          },
        );
      },
    );
  }

  Widget _buildBody(BuildContext context, DatabaseDetail detail) {
    final d = detail.instance;
    final status = databaseStatus(d);
    return RefreshIndicator(
      onRefresh: () async => _refresh(),
      child: ListView(
        padding: const EdgeInsets.all(Space.lg),
        children: [
          Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 900),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Row(
                    children: [
                      EngineAvatar(category: d.category),
                      const SizedBox(width: Space.md),
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Text(
                              '${d.engineName} ${d.version}',
                              style: Theme.of(context).textTheme.titleLarge,
                            ),
                            Text(
                              '${d.serverName} · ${d.profile}'
                              '${d.highAvailability ? ' · high availability' : ''}',
                              style: Theme.of(context).textTheme.bodySmall,
                            ),
                          ],
                        ),
                      ),
                      StatusPill(label: status.label, tone: status.tone),
                    ],
                  ),
                  if (status.tone == StatusTone.failed) ...[
                    const SizedBox(height: Space.md),
                    NoticeBanner(
                      tone: StatusTone.failed,
                      icon: Icons.error_outline,
                      title: 'Deployment needs attention',
                      message:
                          'Open the deployment for its event timeline and '
                          'container logs.',
                      actionLabel: 'Open deployment',
                      onAction: () => _openDeployment(d),
                    ),
                  ],
                  const SizedBox(height: Space.lg),
                  _buildConnection(context, d),
                  const SizedBox(height: Space.lg),
                  _buildCredentials(context, d),
                  if (d.persistent) ...[
                    const SizedBox(height: Space.lg),
                    _buildBackups(context, d, detail.backups),
                  ],
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildConnection(BuildContext context, DatabaseInstance d) {
    return SectionCard(
      title: 'Connection',
      icon: Icons.cable_outlined,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          KeyValueRow(label: 'Host', value: d.host, monospace: true),
          KeyValueRow(label: 'Port', value: '${d.port}', monospace: true),
          for (final p in d.extraPorts)
            KeyValueRow(label: p.name, value: '${p.host}', monospace: true),
          if (d.databaseName.isNotEmpty)
            KeyValueRow(
              label: 'Database',
              value: d.databaseName,
              monospace: true,
            ),
          if (d.adminUsername.isNotEmpty)
            KeyValueRow(
              label: 'Username',
              value: d.adminUsername,
              monospace: true,
            ),
          if (d.connectionString.isNotEmpty)
            KeyValueRow(
              label: 'URI',
              value: d.connectionString,
              monospace: true,
              trailing: IconButton(
                tooltip: 'Copy URI (without password)',
                icon: const Icon(Icons.copy, size: 18),
                onPressed: () {
                  Clipboard.setData(ClipboardData(text: d.connectionString));
                  _snack('Copied.');
                },
              ),
            ),
          KeyValueRow(
            label: 'Resources',
            value:
                '${d.memoryMb} MB · ${d.cpus} CPU'
                '${d.persistent ? ' · ${d.storageGb} GB storage' : ''}',
          ),
          const SizedBox(height: Space.sm),
          Text(
            d.access == 'local'
                ? 'Local only: reachable on the server itself (127.0.0.1) or '
                      'through an SSH tunnel, e.g. ssh -L ${d.port}:127.0.0.1:${d.port} ${d.serverName}'
                : 'Remote: published on every interface of ${d.serverName}.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ],
      ),
    );
  }

  Widget _buildCredentials(BuildContext context, DatabaseInstance d) {
    return SectionCard(
      title: 'Credentials',
      icon: Icons.key_outlined,
      actions: [
        if (d.temporaryUsers)
          TextButton.icon(
            onPressed: _busy.contains('temporary')
                ? null
                : () => _issueTemporary(d),
            icon: const Icon(Icons.timer_outlined, size: 18),
            label: const Text('Temporary login'),
          ),
      ],
      child: FutureBuilder<List<DatabaseCredential>>(
        future: _credentialsFuture,
        builder: (context, snapshot) {
          if (snapshot.hasError) {
            return Text(
              'Couldn\'t load credentials: ${_error(snapshot.error!)}',
            );
          }
          if (!snapshot.hasData) {
            return const Padding(
              padding: EdgeInsets.all(Space.md),
              child: Center(child: CircularProgressIndicator()),
            );
          }
          final list = snapshot.data!;
          if (list.isEmpty) {
            return Text(
              '${d.engineName} runs without authentication in this template, '
              'so there are no credentials. Keep its access local.',
              style: Theme.of(context).textTheme.bodySmall,
            );
          }
          return Column(
            children: [
              for (final c in list) _credentialRow(context, d, c),
              const SizedBox(height: Space.sm),
              Text(
                'Values are encrypted in the vault, masked here, masked in '
                'container logs, and every reveal, download, share and '
                'rotation is recorded in the audit log.',
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ],
          );
        },
      ),
    );
  }

  Widget _credentialRow(
    BuildContext context,
    DatabaseInstance d,
    DatabaseCredential c,
  ) {
    final theme = Theme.of(context);
    final revealed = _revealed[c.id];
    final expired = !c.active;
    final canRotate = c.canManage && c.kind == 'admin' && d.rotation != 'none';
    final busy = _busy.any((k) => k.endsWith(c.id));
    final meta = [
      if (c.username.isNotEmpty) c.username,
      'v${c.version}',
      if (c.rotatedAt != null) 'rotated ${formatTimestamp(c.rotatedAt!)}',
      if (c.expiresAt != null)
        (expired ? 'expired ' : 'expires ') + formatTimestamp(c.expiresAt!),
      if (c.revokedAt != null) 'revoked',
    ].join(' · ');

    return Container(
      margin: const EdgeInsets.only(bottom: Space.sm),
      padding: const EdgeInsets.all(Space.md),
      decoration: BoxDecoration(
        borderRadius: BorderRadius.circular(Radii.md),
        border: Border.all(color: theme.colorScheme.outlineVariant),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  c.name,
                  style: theme.textTheme.titleSmall?.copyWith(
                    color: expired ? theme.colorScheme.outline : null,
                  ),
                ),
              ),
              if (c.isTemporary)
                StatusPill(
                  label: expired ? 'Expired' : 'Temporary',
                  tone: expired ? StatusTone.neutral : StatusTone.warning,
                ),
            ],
          ),
          const SizedBox(height: 2),
          Text(meta, style: theme.textTheme.bodySmall),
          const SizedBox(height: Space.sm),
          Row(
            children: [
              Expanded(
                child: SelectableText(
                  revealed ?? c.masked,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    fontFamily: 'monospace',
                    letterSpacing: revealed == null ? 2 : 0,
                  ),
                ),
              ),
              if (busy)
                const Padding(
                  padding: EdgeInsets.all(Space.sm),
                  child: SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                ),
              if (c.canReveal) ...[
                IconButton(
                  tooltip: revealed == null ? 'Reveal for 30 seconds' : 'Hide',
                  icon: Icon(
                    revealed == null
                        ? Icons.visibility_outlined
                        : Icons.visibility_off_outlined,
                    size: 20,
                  ),
                  onPressed: busy
                      ? null
                      : () => revealed == null
                            ? _withBusy('reveal-${c.id}', () => _reveal(c))
                            : _hide(c),
                ),
                IconButton(
                  tooltip: 'Copy',
                  icon: const Icon(Icons.copy, size: 20),
                  onPressed: busy ? null : () => _copy(c),
                ),
              ],
              PopupMenuButton<String>(
                tooltip: 'More',
                enabled: !busy,
                onSelected: (v) => switch (v) {
                  'download' => _download(c),
                  'rotate' => _rotate(d, c),
                  'share' => _share(c),
                  'revoke' => _revoke(c),
                  _ => null,
                },
                itemBuilder: (_) => [
                  PopupMenuItem(
                    value: 'download',
                    enabled: c.canReveal && c.downloadedAt == null,
                    child: ListTile(
                      leading: const Icon(Icons.download_outlined),
                      title: const Text('Download once'),
                      subtitle: c.downloadedAt != null
                          ? Text(
                              'Downloaded ${formatTimestamp(c.downloadedAt!)}',
                            )
                          : null,
                    ),
                  ),
                  if (canRotate)
                    const PopupMenuItem(
                      value: 'rotate',
                      child: ListTile(
                        leading: Icon(Icons.autorenew),
                        title: Text('Rotate'),
                      ),
                    ),
                  if (c.canManage && c.active)
                    const PopupMenuItem(
                      value: 'share',
                      child: ListTile(
                        leading: Icon(Icons.group_add_outlined),
                        title: Text('Share…'),
                      ),
                    ),
                  if (c.canManage && c.isTemporary && c.revokedAt == null)
                    const PopupMenuItem(
                      value: 'revoke',
                      child: ListTile(
                        leading: Icon(Icons.block),
                        title: Text('Revoke now'),
                      ),
                    ),
                ],
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _buildBackups(
    BuildContext context,
    DatabaseInstance d,
    List<Backup> backups,
  ) {
    final theme = Theme.of(context);
    return SectionCard(
      title: 'Backups',
      icon: Icons.backup_outlined,
      actions: [
        TextButton.icon(
          onPressed: () => _editPolicy(d),
          icon: const Icon(Icons.edit_calendar_outlined, size: 18),
          label: const Text('Policy'),
        ),
        FilledButton.tonalIcon(
          onPressed: _busy.contains('backup') ? null : () => _backupNow(d),
          icon: const Icon(Icons.backup, size: 18),
          label: const Text('Back up now'),
        ),
      ],
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          KeyValueRow(
            label: 'Schedule',
            value: describeBackupCron(d.backupCron),
          ),
          if (d.backupNextRunAt != null && d.backupCron != null)
            KeyValueRow(
              label: 'Next run',
              value: formatTimestamp(d.backupNextRunAt!),
            ),
          if (d.backupLastRunAt != null)
            KeyValueRow(
              label: 'Last scheduled',
              value:
                  '${formatTimestamp(d.backupLastRunAt!)} — ${d.backupLastStatus ?? ''}',
            ),
          KeyValueRow(
            label: 'Retention',
            value: describeRetention(d.retentionDays, d.retentionCount),
          ),
          KeyValueRow(
            label: 'Mode',
            value: d.backupConsistent
                ? 'Consistent (brief pause during snapshot)'
                : 'Live snapshot (no pause)',
          ),
          const Divider(height: Space.xl),
          if (backups.isEmpty)
            Text('No backups yet.', style: theme.textTheme.bodySmall)
          else
            for (final b in backups.take(10))
              ListTile(
                dense: true,
                contentPadding: EdgeInsets.zero,
                leading: Icon(
                  b.origin == 'scheduled'
                      ? Icons.schedule
                      : Icons.touch_app_outlined,
                  size: 20,
                ),
                title: Text(formatTimestamp(b.createdAt)),
                subtitle: Text(
                  [
                    b.origin == 'scheduled' ? 'Scheduled' : 'Manual',
                    if (b.sizeBytes != null) formatBytes(b.sizeBytes!),
                    if (b.message.isNotEmpty) b.message,
                  ].join(' · '),
                ),
                trailing: StatusPill(
                  label: switch (b.status) {
                    'completed' => 'Completed',
                    'failed' => 'Failed',
                    'running' => 'Running',
                    _ => 'Pending',
                  },
                  tone: switch (b.status) {
                    'completed' => StatusTone.healthy,
                    'failed' => StatusTone.failed,
                    _ => StatusTone.warning,
                  },
                ),
              ),
          if (backups.length > 10)
            Text(
              '${backups.length - 10} older backups — see the deployment\'s backups screen.',
              style: theme.textTheme.bodySmall,
            ),
        ],
      ),
    );
  }
}
