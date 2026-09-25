import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../models/compose_file.dart';
import '../../models/deployment.dart';
import '../../models/git_repository.dart';
import '../../theme/app_theme.dart';

/// Git-based deployment: repositories Compose files can be imported from,
/// their push-webhook setup, and the Compose files linked to each (with
/// manual sync). Deploying a linked file from a branch/tag, auto-deploy on
/// push, and rollback to a commit live on the deploy dialog and the
/// deployment screen.
class GitRepositoriesScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;
  // Called after a Compose file is imported or synced, so the Compose
  // files tab can refresh.
  final VoidCallback? onComposeFilesChanged;

  const GitRepositoriesScreen({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
    this.onComposeFilesChanged,
  });

  @override
  State<GitRepositoriesScreen> createState() => _GitRepositoriesScreenState();
}

class _GitRepositoriesScreenState extends State<GitRepositoriesScreen> {
  late Future<(List<GitRepository>, List<ComposeFile>)> _future = _load();
  bool _busy = false;

  ApiClient get _api => widget.apiClient;

  Future<(List<GitRepository>, List<ComposeFile>)> _load() async {
    final results = await Future.wait([
      _api.listGitRepositories(),
      _api.listComposeFiles(),
    ]);
    return (results[0] as List<GitRepository>, results[1] as List<ComposeFile>);
  }

  void _refresh() => setState(() {
    _future = _load();
  });

  void _snack(String text) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
  }

  String _err(Object e) => e is ApiException ? e.message : '$e';

  Future<void> _addOrEdit([GitRepository? existing]) async {
    final result = await showDialog<Object>(
      context: context,
      builder: (_) => _RepositoryDialog(apiClient: _api, existing: existing),
    );
    if (result == null || !mounted) return;
    if (result is GitWebhookInfo) {
      await showDialog<void>(
        context: context,
        builder: (_) => _WebhookDialog(info: result),
      );
    }
    _refresh();
  }

  Future<void> _delete(GitRepository repo) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Remove ${repo.name}?'),
        content: const Text(
          'Linked Compose files keep their current content but stop syncing.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.error,
            ),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await _api.deleteGitRepository(repo.id);
      _refresh();
      widget.onComposeFilesChanged?.call();
    } catch (e) {
      _snack('Failed to remove: ${_err(e)}');
    }
  }

  Future<void> _showWebhook(GitRepository repo) async {
    try {
      final info = await _api.getGitWebhook(repo.id);
      if (!mounted) return;
      final rotate = await showDialog<bool>(
        context: context,
        builder: (_) => _WebhookDialog(info: info, canRotate: true),
      );
      if (rotate == true) {
        final rotated = await _api.rotateGitWebhookSecret(repo.id);
        if (!mounted) return;
        await showDialog<void>(
          context: context,
          builder: (_) => _WebhookDialog(info: rotated),
        );
      }
    } catch (e) {
      _snack('Failed to load webhook: ${_err(e)}');
    }
  }

  Future<void> _import(GitRepository repo) async {
    final imported = await showDialog<ComposeFile>(
      context: context,
      builder: (_) => ImportFromGitDialog(apiClient: _api, repository: repo),
    );
    if (imported != null) {
      _snack(
        'Imported ${imported.name} from ${imported.gitRef} @ ${shortCommit(imported.gitCommit)}.',
      );
      _refresh();
      widget.onComposeFilesChanged?.call();
    }
  }

  Future<void> _sync(ComposeFile file) async {
    setState(() => _busy = true);
    try {
      final r = await _api.syncComposeFile(file.id);
      _snack(
        r.changed
            ? '${file.name} updated to ${shortCommit(r.commit)} (now v${r.file.version}).'
            : '${file.name} is already at the latest commit (${shortCommit(r.commit)}).',
      );
      _refresh();
      widget.onComposeFilesChanged?.call();
    } catch (e) {
      _snack('Sync failed: ${_err(e)}');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      floatingActionButton: widget.isAdmin
          ? FloatingActionButton.extended(
              // Shares a TabBarView with the Compose files tab's button.
              heroTag: 'git-add-repository',
              onPressed: () => _addOrEdit(),
              icon: const Icon(Icons.add),
              label: const Text('Add repository'),
            )
          : null,
      body: FutureBuilder<(List<GitRepository>, List<ComposeFile>)>(
        future: _future,
        builder: (context, snapshot) {
          if (snapshot.connectionState != ConnectionState.done) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(
              child: Text('Failed to load repositories: ${snapshot.error}'),
            );
          }
          final (repos, files) = snapshot.data!;
          if (repos.isEmpty) {
            return Center(
              child: Text(
                widget.isAdmin
                    ? 'No Git repositories yet.\nAdd one to import Compose '
                          'files from GitHub, GitLab, Azure DevOps, or Bitbucket.'
                    : 'No Git repositories yet. Ask an admin to add one.',
                textAlign: TextAlign.center,
              ),
            );
          }
          return ListView(
            padding: const EdgeInsets.fromLTRB(12, 12, 12, 88),
            children: [
              if (_busy) const LinearProgressIndicator(),
              for (final repo in repos)
                _RepositoryCard(
                  repository: repo,
                  linkedFiles: [
                    for (final f in files)
                      if (f.gitRepositoryId == repo.id) f,
                  ],
                  isAdmin: widget.isAdmin,
                  onImport: () => _import(repo),
                  onEdit: () => _addOrEdit(repo),
                  onDelete: () => _delete(repo),
                  onWebhook: () => _showWebhook(repo),
                  onSync: _busy ? null : _sync,
                ),
            ],
          );
        },
      ),
    );
  }
}

class _RepositoryCard extends StatelessWidget {
  final GitRepository repository;
  final List<ComposeFile> linkedFiles;
  final bool isAdmin;
  final VoidCallback onImport;
  final VoidCallback onEdit;
  final VoidCallback onDelete;
  final VoidCallback onWebhook;
  final ValueChanged<ComposeFile>? onSync;

  const _RepositoryCard({
    required this.repository,
    required this.linkedFiles,
    required this.isAdmin,
    required this.onImport,
    required this.onEdit,
    required this.onDelete,
    required this.onWebhook,
    required this.onSync,
  });

  @override
  Widget build(BuildContext context) {
    final r = repository;
    return Card(
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            ListTile(
              leading: const Icon(Icons.source_outlined),
              title: Text(r.name),
              subtitle: Text(
                '${r.providerLabel} • ${r.url}\n'
                'Default branch ${r.defaultBranch}'
                '${r.hasToken ? ' • credentials configured' : ' • public (no credentials)'}',
              ),
              isThreeLine: true,
              trailing: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextButton.icon(
                    onPressed: onImport,
                    icon: const Icon(Icons.download_outlined),
                    label: const Text('Import Compose file'),
                  ),
                  if (isAdmin)
                    PopupMenuButton<String>(
                      onSelected: (v) {
                        switch (v) {
                          case 'webhook':
                            onWebhook();
                          case 'edit':
                            onEdit();
                          case 'delete':
                            onDelete();
                        }
                      },
                      itemBuilder: (_) => const [
                        PopupMenuItem(
                          value: 'webhook',
                          child: Text('Webhook setup'),
                        ),
                        PopupMenuItem(value: 'edit', child: Text('Edit')),
                        PopupMenuDivider(),
                        PopupMenuItem(value: 'delete', child: Text('Remove')),
                      ],
                    ),
                ],
              ),
            ),
            if (linkedFiles.isEmpty)
              Padding(
                padding: const EdgeInsets.fromLTRB(72, 0, 16, 12),
                child: Text(
                  'No Compose files imported from this repository yet.',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ),
            for (final f in linkedFiles)
              ListTile(
                dense: true,
                contentPadding: const EdgeInsets.only(left: 72, right: 16),
                leading: const Icon(Icons.layers_outlined, size: 18),
                title: Text(f.name),
                subtitle: Text(
                  '${f.gitPath} @ ${f.gitRef} • ${shortCommit(f.gitCommit)}'
                  '${f.gitSyncedAt == null ? '' : ' • synced ${formatTimestamp(f.gitSyncedAt!)}'}',
                ),
                trailing: IconButton(
                  icon: const Icon(Icons.sync),
                  tooltip: 'Sync from Git',
                  onPressed: onSync == null ? null : () => onSync!(f),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// Add/edit a repository. Pops a [GitWebhookInfo] after creating one (so
/// the webhook can be configured), or `true` after an edit.
class _RepositoryDialog extends StatefulWidget {
  final ApiClient apiClient;
  final GitRepository? existing;

  const _RepositoryDialog({required this.apiClient, this.existing});

  @override
  State<_RepositoryDialog> createState() => _RepositoryDialogState();
}

class _RepositoryDialogState extends State<_RepositoryDialog> {
  late final _name = TextEditingController(text: widget.existing?.name);
  late final _url = TextEditingController(text: widget.existing?.url);
  late final _username = TextEditingController(text: widget.existing?.username);
  final _token = TextEditingController();
  late final _branch = TextEditingController(
    text: widget.existing?.defaultBranch ?? 'main',
  );
  late String _provider = widget.existing?.provider ?? 'github';
  bool _saving = false;
  String? _error;

  @override
  void dispose() {
    for (final c in [_name, _url, _username, _token, _branch]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> _save() async {
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      final existing = widget.existing;
      if (existing == null) {
        final (_, webhook) = await widget.apiClient.createGitRepository(
          name: _name.text.trim(),
          provider: _provider,
          url: _url.text.trim(),
          username: _username.text.trim(),
          token: _token.text.trim(),
          defaultBranch: _branch.text.trim(),
        );
        if (mounted) Navigator.of(context).pop(webhook);
      } else {
        await widget.apiClient.updateGitRepository(
          existing.id,
          name: _name.text.trim(),
          provider: _provider,
          url: _url.text.trim(),
          username: _username.text.trim(),
          token: _token.text.trim().isEmpty ? null : _token.text.trim(),
          defaultBranch: _branch.text.trim(),
        );
        if (mounted) Navigator.of(context).pop(true);
      }
    } catch (e) {
      setState(() => _error = e is ApiException ? e.message : '$e');
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final editing = widget.existing != null;
    return AlertDialog(
      title: Text(editing ? 'Edit repository' : 'Add Git repository'),
      content: SizedBox(
        width: 480,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TextField(
                controller: _name,
                decoration: const InputDecoration(
                  labelText: 'Name',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: _provider,
                decoration: const InputDecoration(
                  labelText: 'Provider',
                  isDense: true,
                ),
                items: [
                  for (final e in kGitProviders.entries)
                    DropdownMenuItem(value: e.key, child: Text(e.value)),
                ],
                onChanged: (v) => setState(() => _provider = v ?? 'generic'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _url,
                decoration: const InputDecoration(
                  labelText: 'HTTPS clone URL',
                  hintText: 'https://github.com/org/repo.git',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _branch,
                decoration: const InputDecoration(
                  labelText: 'Default branch',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _username,
                decoration: const InputDecoration(
                  labelText: 'Username (optional)',
                  hintText: 'Leave empty to use the provider default',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _token,
                obscureText: true,
                decoration: InputDecoration(
                  labelText: editing
                      ? 'Access token (leave empty to keep the current one)'
                      : 'Access token (for private repositories)',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                'The repository is checked with these credentials before it is saved.',
                style: Theme.of(context).textTheme.bodySmall,
              ),
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(_error!, style: const TextStyle(color: AppColors.failed)),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
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
              : Text(editing ? 'Save' : 'Add'),
        ),
      ],
    );
  }
}

/// Shows how to configure a repository's push webhook. Pops `true` when
/// the user asks to rotate the secret.
class _WebhookDialog extends StatelessWidget {
  final GitWebhookInfo info;
  final bool canRotate;

  const _WebhookDialog({required this.info, this.canRotate = false});

  @override
  Widget build(BuildContext context) {
    Widget row(String label, String value) => Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          SizedBox(width: 70, child: Text(label)),
          Expanded(
            child: SelectableText(
              value,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
            ),
          ),
          IconButton(
            icon: const Icon(Icons.copy, size: 18),
            tooltip: 'Copy',
            onPressed: () => Clipboard.setData(ClipboardData(text: value)),
          ),
        ],
      ),
    );
    return AlertDialog(
      title: const Text('Push webhook'),
      content: SizedBox(
        width: 560,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            const Text(
              'Add a push webhook in your Git host pointing at this URL. '
              'Pushes re-sync linked Compose files and redeploy deployments '
              'that have auto-deploy on.',
            ),
            const SizedBox(height: 12),
            row('URL', info.url),
            row('Secret', info.secret),
            const SizedBox(height: 8),
            Text(
              'GitHub / Bitbucket: set the secret (HMAC-SHA256 signature). '
              'GitLab: use it as the secret token. Azure DevOps: append '
              '?secret=<secret> to the URL or use it as the basic-auth password.',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
        ),
      ),
      actions: [
        if (canRotate)
          TextButton(
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Rotate secret'),
          ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Done'),
        ),
      ],
    );
  }
}

/// "Import Compose files from Git": pick a branch or tag and a path,
/// preview the file with its validation, then import it as a linked
/// Compose file.
class ImportFromGitDialog extends StatefulWidget {
  final ApiClient apiClient;
  final GitRepository repository;

  const ImportFromGitDialog({
    super.key,
    required this.apiClient,
    required this.repository,
  });

  @override
  State<ImportFromGitDialog> createState() => _ImportFromGitDialogState();
}

class _ImportFromGitDialogState extends State<ImportFromGitDialog> {
  late final Future<GitRefs> _refs = widget.apiClient.listGitRefs(
    widget.repository.id,
  );
  late String _ref = widget.repository.defaultBranch;
  final _path = TextEditingController(text: 'compose.yaml');
  final _name = TextEditingController();
  ({String content, String commit, ComposeParseResult parse})? _preview;
  bool _working = false;
  String? _error;

  @override
  void dispose() {
    _path.dispose();
    _name.dispose();
    super.dispose();
  }

  Future<void> _loadPreview() async {
    setState(() {
      _working = true;
      _error = null;
      _preview = null;
    });
    try {
      final p = await widget.apiClient.previewGitFile(
        widget.repository.id,
        ref: _ref,
        path: _path.text.trim(),
      );
      setState(() => _preview = p);
      if (_name.text.trim().isEmpty) {
        final segments = _path.text.trim().split('/');
        _name.text = segments.length > 1
            ? segments[segments.length - 2]
            : widget.repository.name;
      }
    } catch (e) {
      setState(() => _error = e is ApiException ? e.message : '$e');
    } finally {
      if (mounted) setState(() => _working = false);
    }
  }

  Future<void> _import() async {
    setState(() {
      _working = true;
      _error = null;
    });
    try {
      final file = await widget.apiClient.importGitComposeFile(
        widget.repository.id,
        name: _name.text.trim(),
        ref: _ref,
        path: _path.text.trim(),
      );
      if (mounted) Navigator.of(context).pop(file);
    } catch (e) {
      setState(() => _error = e is ApiException ? e.message : '$e');
    } finally {
      if (mounted) setState(() => _working = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final preview = _preview;
    return AlertDialog(
      title: Text('Import from ${widget.repository.name}'),
      content: SizedBox(
        width: 620,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              FutureBuilder<GitRefs>(
                future: _refs,
                builder: (context, snapshot) {
                  if (snapshot.hasError) {
                    return Text(
                      'Couldn\'t list branches: ${snapshot.error}',
                      style: const TextStyle(color: AppColors.failed),
                    );
                  }
                  final refs = snapshot.data;
                  if (refs == null) return const LinearProgressIndicator();
                  final names = [
                    for (final b in refs.branches) b.name,
                    for (final t in refs.tags) t.name,
                  ];
                  if (!names.contains(_ref) && names.isNotEmpty) {
                    _ref = names.first;
                  }
                  return DropdownButtonFormField<String>(
                    initialValue: names.contains(_ref) ? _ref : null,
                    isExpanded: true,
                    decoration: const InputDecoration(
                      labelText: 'Branch or tag',
                      isDense: true,
                    ),
                    items: [
                      for (final b in refs.branches)
                        DropdownMenuItem(
                          value: b.name,
                          child: Text('${b.name}  (branch)'),
                        ),
                      for (final t in refs.tags)
                        DropdownMenuItem(
                          value: t.name,
                          child: Text('${t.name}  (tag)'),
                        ),
                    ],
                    onChanged: (v) => setState(() {
                      _ref = v ?? _ref;
                      _preview = null;
                    }),
                  );
                },
              ),
              const SizedBox(height: 12),
              Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: _path,
                      decoration: const InputDecoration(
                        labelText: 'Path to the Compose file',
                        hintText: 'deploy/compose.yaml',
                        isDense: true,
                      ),
                      onChanged: (_) => setState(() => _preview = null),
                    ),
                  ),
                  const SizedBox(width: 12),
                  OutlinedButton(
                    onPressed: _working ? null : _loadPreview,
                    child: const Text('Preview'),
                  ),
                ],
              ),
              if (preview != null) ...[
                const SizedBox(height: 12),
                Text(
                  'Commit ${shortCommit(preview.commit)} • '
                  '${preview.parse.valid ? 'valid Compose file' : 'invalid'}'
                  '${preview.parse.serviceNames.isEmpty ? '' : ' • services: ${preview.parse.serviceNames.join(', ')}'}',
                  style: TextStyle(
                    color: preview.parse.valid ? AppColors.healthy : AppColors.failed,
                  ),
                ),
                for (final e in preview.parse.errors)
                  Text('• $e', style: const TextStyle(color: AppColors.failed)),
                for (final w in preview.parse.warnings)
                  Text('• $w', style: const TextStyle(color: AppColors.warning)),
                const SizedBox(height: 8),
                Container(
                  height: 220,
                  padding: const EdgeInsets.all(8),
                  decoration: BoxDecoration(
                    border: Border.all(color: Theme.of(context).dividerColor),
                  ),
                  child: SingleChildScrollView(
                    child: SelectableText(
                      preview.content,
                      style: const TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 12,
                      ),
                    ),
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: _name,
                  decoration: const InputDecoration(
                    labelText: 'Name for the imported Compose file',
                    isDense: true,
                  ),
                  onChanged: (_) => setState(() {}),
                ),
              ],
              if (_working) ...[
                const SizedBox(height: 12),
                const LinearProgressIndicator(),
              ],
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(_error!, style: const TextStyle(color: AppColors.failed)),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed:
              _working ||
                  preview == null ||
                  !preview.parse.valid ||
                  _name.text.trim().isEmpty
              ? null
              : _import,
          child: const Text('Import'),
        ),
      ],
    );
  }
}
