import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../models/database.dart';
import '../../models/deployment.dart' show formatTimestamp;
import '../../theme/app_theme.dart';
import '../../widgets/notice_banner.dart';
import '../../widgets/status_pill.dart';
import 'database_widgets.dart';

/// A yes/no confirmation. [destructive] tints the confirm button.
Future<bool> confirm(
  BuildContext context, {
  required String title,
  required String message,
  required String confirmLabel,
  bool destructive = false,
}) async {
  final result = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text(title),
      content: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 460),
        child: Text(message),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          style: destructive
              ? FilledButton.styleFrom(
                  backgroundColor: Theme.of(context).colorScheme.error,
                  foregroundColor: Theme.of(context).colorScheme.onError,
                )
              : null,
          onPressed: () => Navigator.of(context).pop(true),
          child: Text(confirmLabel),
        ),
      ],
    ),
  );
  return result ?? false;
}

/// Shows the one-time credentials file. It can't be fetched again, so the
/// dialog says so and offers a copy.
Future<void> showCredentialsFileDialog(BuildContext context, String text) {
  return showDialog<void>(
    context: context,
    barrierDismissible: false,
    builder: (context) => AlertDialog(
      title: const Text('Credentials file'),
      content: SizedBox(
        width: 600,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const NoticeBanner(
              tone: StatusTone.warning,
              icon: Icons.save_outlined,
              title: 'Shown once',
              message:
                  'Copy this into a password manager or a .env file now. It '
                  'can\'t be downloaded again until the credential is rotated.',
            ),
            const SizedBox(height: Space.md),
            Container(
              padding: const EdgeInsets.all(Space.md),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerLowest,
                borderRadius: BorderRadius.circular(Radii.sm),
              ),
              child: SelectableText(
                text,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton.icon(
          onPressed: () => Clipboard.setData(ClipboardData(text: text)),
          icon: const Icon(Icons.copy, size: 18),
          label: const Text('Copy'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('I\'ve saved it'),
        ),
      ],
    ),
  );
}

/// The rare rotation where the database changed but the vault didn't: the
/// new password exists only here.
Future<void> showUnsavedPasswordDialog(BuildContext context, String password) {
  return showDialog<void>(
    context: context,
    barrierDismissible: false,
    builder: (context) => AlertDialog(
      title: const Text('Save this password now'),
      content: SizedBox(
        width: 480,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Text(
              'The database now uses a new password, but storing it in the '
              'vault failed. This is the only copy — save it, then contact '
              'an administrator to update the stored credential.',
            ),
            const SizedBox(height: Space.md),
            SelectableText(
              password,
              style: const TextStyle(fontFamily: 'monospace'),
            ),
          ],
        ),
      ),
      actions: [
        TextButton.icon(
          onPressed: () => Clipboard.setData(ClipboardData(text: password)),
          icon: const Icon(Icons.copy, size: 18),
          label: const Text('Copy'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Done'),
        ),
      ],
    ),
  );
}

/// Asks how long a temporary read-only login should live.
Future<Duration?> showTemporaryCredentialDialog(BuildContext context) {
  final options = <Duration, String>{
    Duration(hours: 1): '1 hour',
    Duration(hours: 8): '8 hours',
    Duration(days: 1): '1 day',
    Duration(days: 7): '7 days',
    Duration(days: 30): '30 days',
  };
  var selected = const Duration(hours: 8);
  return showDialog<Duration>(
    context: context,
    builder: (context) => StatefulBuilder(
      builder: (context, setState) => AlertDialog(
        title: const Text('Issue a temporary login'),
        content: SizedBox(
          width: 420,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                'Creates a new read-only database user with a generated '
                'password. It\'s dropped from the database automatically '
                'when it expires, or when you revoke it.',
              ),
              const SizedBox(height: Space.md),
              Wrap(
                spacing: Space.sm,
                runSpacing: Space.sm,
                children: [
                  for (final o in options.entries)
                    ChoiceChip(
                      label: Text(o.value),
                      selected: selected == o.key,
                      onSelected: (_) => setState(() => selected = o.key),
                    ),
                ],
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(selected),
            child: const Text('Issue'),
          ),
        ],
      ),
    ),
  );
}

/// Who a credential is shared with, and sharing it with someone else —
/// permanently or until a chosen time.
Future<void> showShareDialog(
  BuildContext context, {
  required ApiClient api,
  required DatabaseCredential credential,
}) {
  return showDialog<void>(
    context: context,
    builder: (_) => _ShareDialog(api: api, credential: credential),
  );
}

class _ShareDialog extends StatefulWidget {
  final ApiClient api;
  final DatabaseCredential credential;

  const _ShareDialog({required this.api, required this.credential});

  @override
  State<_ShareDialog> createState() => _ShareDialogState();
}

class _ShareDialogState extends State<_ShareDialog> {
  late Future<List<SecretGrant>> _grants = widget.api.listSecretGrants(
    widget.credential.id,
  );
  late final Future<List<DirectoryUser>> _users = widget.api
      .listUserDirectory();
  String? _userId;
  Duration? _expiry = const Duration(days: 7);
  bool _saving = false;
  String? _error;

  static final _expiries = <Duration?, String>{
    Duration(days: 1): '1 day',
    Duration(days: 7): '7 days',
    Duration(days: 30): '30 days',
    null: 'No expiry',
  };

  Future<void> _share() async {
    if (_userId == null) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      final grants = await widget.api.shareSecret(
        widget.credential.id,
        _userId!,
        expiresAt: _expiry == null ? null : DateTime.now().add(_expiry!),
      );
      setState(() {
        _grants = Future.value(grants);
        _userId = null;
      });
    } catch (e) {
      setState(() => _error = e is ApiException ? e.message : '$e');
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Future<void> _unshare(SecretGrant g) async {
    try {
      await widget.api.unshareSecret(widget.credential.id, g.userId);
      setState(() {
        _grants = widget.api.listSecretGrants(widget.credential.id);
      });
    } catch (e) {
      setState(() => _error = e is ApiException ? e.message : '$e');
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return AlertDialog(
      title: Text('Share ${widget.credential.name.toLowerCase()}'),
      content: SizedBox(
        width: 520,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              'People you share with can reveal, copy and download this '
              'credential. Only you and admins can rotate it or change who '
              'it\'s shared with.',
              style: theme.textTheme.bodySmall,
            ),
            const SizedBox(height: Space.md),
            FutureBuilder<List<SecretGrant>>(
              future: _grants,
              builder: (context, snapshot) {
                final grants = snapshot.data ?? [];
                if (!snapshot.hasData) {
                  return const LinearProgressIndicator();
                }
                if (grants.isEmpty) {
                  return Text(
                    'Not shared with anyone.',
                    style: theme.textTheme.bodyMedium,
                  );
                }
                return Column(
                  children: [
                    for (final g in grants)
                      ListTile(
                        dense: true,
                        contentPadding: EdgeInsets.zero,
                        leading: const Icon(Icons.person_outline),
                        title: Text(g.email),
                        subtitle: Text(
                          g.expiresAt == null
                              ? 'No expiry'
                              : g.isExpired()
                              ? 'Expired ${formatTimestamp(g.expiresAt!)}'
                              : 'Until ${formatTimestamp(g.expiresAt!)}',
                        ),
                        trailing: IconButton(
                          tooltip: 'Stop sharing',
                          icon: const Icon(Icons.close),
                          onPressed: () => _unshare(g),
                        ),
                      ),
                  ],
                );
              },
            ),
            const Divider(height: Space.xl),
            FutureBuilder<List<DirectoryUser>>(
              future: _users,
              builder: (context, snapshot) {
                final users = (snapshot.data ?? [])
                    .where((u) => u.id != widget.credential.ownerId)
                    .toList();
                return DropdownButtonFormField<String>(
                  isExpanded: true,
                  initialValue: _userId,
                  decoration: const InputDecoration(labelText: 'Share with'),
                  items: [
                    for (final u in users)
                      DropdownMenuItem(value: u.id, child: Text(u.email)),
                  ],
                  onChanged: (v) => setState(() => _userId = v),
                );
              },
            ),
            const SizedBox(height: Space.md),
            Wrap(
              spacing: Space.sm,
              children: [
                for (final e in _expiries.entries)
                  ChoiceChip(
                    label: Text(e.value),
                    selected: _expiry == e.key,
                    onSelected: (_) => setState(() => _expiry = e.key),
                  ),
              ],
            ),
            if (_error != null) ...[
              const SizedBox(height: Space.md),
              Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
        FilledButton(
          onPressed: _userId == null || _saving ? null : _share,
          child: const Text('Share'),
        ),
      ],
    );
  }
}

/// Edits a database's backup schedule, consistency and retention. Pops
/// true when saved.
Future<bool?> showBackupPolicyDialog(
  BuildContext context, {
  required ApiClient api,
  required DatabaseInstance instance,
}) {
  return showDialog<bool>(
    context: context,
    builder: (_) => _BackupPolicyDialog(api: api, instance: instance),
  );
}

class _BackupPolicyDialog extends StatefulWidget {
  final ApiClient api;
  final DatabaseInstance instance;

  const _BackupPolicyDialog({required this.api, required this.instance});

  @override
  State<_BackupPolicyDialog> createState() => _BackupPolicyDialogState();
}

class _BackupPolicyDialogState extends State<_BackupPolicyDialog> {
  late String _cron = widget.instance.backupCron ?? '';
  late bool _consistent = widget.instance.backupConsistent;
  late final _days = TextEditingController(
    text: '${widget.instance.retentionDays}',
  );
  late final _count = TextEditingController(
    text: '${widget.instance.retentionCount}',
  );
  bool _saving = false;
  String? _error;

  @override
  void dispose() {
    _days.dispose();
    _count.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      await widget.api.updateDatabaseBackupPolicy(
        widget.instance.id,
        cron: _cron,
        consistent: _consistent,
        retentionDays: int.tryParse(_days.text) ?? 0,
        retentionCount: int.tryParse(_count.text) ?? 0,
      );
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() {
        _saving = false;
        _error = e is ApiException ? e.message : '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final presets = {
      ...backupPresets,
      // Keep a custom schedule set through the API selectable.
      if (!backupPresets.containsKey(_cron)) _cron: '$_cron (UTC)',
    };
    return AlertDialog(
      title: const Text('Backup policy'),
      content: SizedBox(
        width: 460,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            DropdownButtonFormField<String>(
              isExpanded: true,
              initialValue: _cron,
              decoration: const InputDecoration(labelText: 'Schedule'),
              items: [
                for (final p in presets.entries)
                  DropdownMenuItem(value: p.key, child: Text(p.value)),
              ],
              onChanged: (v) => setState(() => _cron = v ?? ''),
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              value: _consistent,
              onChanged: (v) => setState(() => _consistent = v),
              title: const Text('Consistent backups'),
              subtitle: const Text('Pause the database during the snapshot'),
            ),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _days,
                    keyboardType: TextInputType.number,
                    inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                    decoration: const InputDecoration(
                      labelText: 'Delete after (days)',
                      helperText: '0 = never',
                    ),
                  ),
                ),
                const SizedBox(width: Space.md),
                Expanded(
                  child: TextField(
                    controller: _count,
                    keyboardType: TextInputType.number,
                    inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                    decoration: const InputDecoration(
                      labelText: 'Keep newest',
                      helperText: '0 = no limit',
                    ),
                  ),
                ),
              ],
            ),
            if (_error != null) ...[
              const SizedBox(height: Space.md),
              Text(
                _error!,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _saving ? null : _save,
          child: const Text('Save'),
        ),
      ],
    );
  }
}
