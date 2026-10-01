import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../models/compose_file.dart';
import '../../models/deployment.dart';
import '../../models/deployment_preview.dart';
import '../../models/env_var_group.dart';
import '../../models/image.dart' show formatBytes;
import '../../models/server.dart';
import '../../widgets/page_intro.dart';
import '../../widgets/state_message.dart';
import '../deployments/deployment_history_screen.dart';
import '../deployments/deployment_status_screen.dart';
import '../deployments/deployment_widgets.dart';
import '../git/git_repositories_screen.dart';
import '../governance/governance_screen.dart';
import 'compose_editor_screen.dart';
import 'compose_version_history_screen.dart';
import 'env_var_groups_screen.dart';
import 'env_variable_editor.dart';
import '../../theme/app_theme.dart';

/// Deployment Management > Docker Compose. Tabs: Compose files (authoring,
/// validation, deploy), History (every stack deployment and its
/// management), Config (env var groups), Git-based deployment, and
/// Governance (approvals, environment policies, audit trail).
/// Pre-deployment checks have no tab of their own: their results show
/// in the deploy preview, where they're actionable.
class DockerComposeScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const DockerComposeScreen({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
  });

  @override
  State<DockerComposeScreen> createState() => _DockerComposeScreenState();
}

class _DockerComposeScreenState extends State<DockerComposeScreen>
    with SingleTickerProviderStateMixin {
  static const _tabs = [
    'Compose files',
    'History',
    'Config',
    'Git',
    'Governance',
  ];

  late final TabController _tabController;
  // Bumped when another tab (Git import/sync) changes the Compose files,
  // so the Compose files tab reloads.
  int _filesGeneration = 0;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: _tabs.length, vsync: this);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Deployments'),
        bottom: TabBar(
          controller: _tabController,
          isScrollable: true,
          tabAlignment: TabAlignment.start,
          tabs: [for (final t in _tabs) Tab(text: t)],
        ),
      ),
      body: TabBarView(
        controller: _tabController,
        children: [
          _ComposeFilesTab(
            key: ValueKey(_filesGeneration),
            apiClient: widget.apiClient,
            isAdmin: widget.isAdmin,
          ),
          DeploymentHistoryScreen(
            apiClient: widget.apiClient,
            isAdmin: widget.isAdmin,
          ),
          _ConfigurationTab(apiClient: widget.apiClient),
          GitRepositoriesScreen(
            apiClient: widget.apiClient,
            isAdmin: widget.isAdmin,
            onComposeFilesChanged: () => setState(() => _filesGeneration++),
          ),
          GovernanceScreen(
            apiClient: widget.apiClient,
            isAdmin: widget.isAdmin,
          ),
        ],
      ),
    );
  }
}

class _ComposeFilesTab extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const _ComposeFilesTab({
    super.key,
    required this.apiClient,
    this.isAdmin = false,
  });

  @override
  State<_ComposeFilesTab> createState() => _ComposeFilesTabState();
}

class _ComposeFilesTabState extends State<_ComposeFilesTab> {
  late Future<List<ComposeFile>> _filesFuture;

  @override
  void initState() {
    super.initState();
    _filesFuture = widget.apiClient.listComposeFiles();
  }

  void _refresh() {
    setState(() {
      _filesFuture = widget.apiClient.listComposeFiles();
    });
  }

  Future<void> _openCreate() async {
    final created = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => ComposeEditorScreen(apiClient: widget.apiClient),
      ),
    );
    if (created == true) _refresh();
  }

  Future<void> _openEdit(ComposeFile file) async {
    final changed = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) =>
            ComposeEditorScreen(apiClient: widget.apiClient, existing: file),
      ),
    );
    if (changed == true) _refresh();
  }

  Future<void> _deploy(ComposeFile file) async {
    List<Server> servers;
    try {
      servers = await widget.apiClient.listServers();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to load servers: $e')));
      }
      return;
    }
    if (!mounted) return;
    if (servers.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('No servers are enrolled yet.')),
      );
      return;
    }

    final server = await showDialog<Server>(
      context: context,
      builder: (_) => SimpleDialog(
        title: Text('Deploy ${file.name} to...'),
        children: [
          for (final s in servers)
            SimpleDialogOption(
              onPressed: () => Navigator.of(context).pop(s),
              child: Row(
                children: [
                  Icon(
                    s.status == 'online' ? Icons.dns : Icons.dns_outlined,
                    size: 18,
                    color: s.status == 'online' ? AppColors.healthy : null,
                  ),
                  const SizedBox(width: 8),
                  Text(s.name),
                ],
              ),
            ),
        ],
      ),
    );
    if (server == null || !mounted) return;

    final setup = await showDialog<_DeploySetup>(
      context: context,
      builder: (_) =>
          _EnvVarSetupDialog(apiClient: widget.apiClient, file: file),
    );
    if (setup == null || !mounted) return;

    DeploymentPreview preview;
    try {
      preview = await widget.apiClient.previewDeployment(
        composeFileId: file.id,
        serverId: server.id,
        env: setup.env,
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to preview deployment: $e')),
        );
      }
      return;
    }
    if (!mounted) return;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) =>
          _DeployPreviewDialog(preview: preview, serverName: server.name),
    );
    if (confirmed != true || !mounted) return;

    final outcome = await runGatedAction(
      context,
      (gate) => widget.apiClient.createComposeDeployment(
        composeFileId: file.id,
        serverId: server.id,
        env: setup.env,
        metadata: setup.metadata,
        gate: gate,
      ),
      failurePrefix: 'Failed to start deployment',
      showOutcome: false,
    );
    if (outcome == null || !mounted) return;
    if (outcome.isQueued) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(outcome.describe())));
    }
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => DeploymentStatusScreen(
          apiClient: widget.apiClient,
          deploymentId: outcome.deploymentId,
          serviceNames: [for (final svc in preview.services) svc.name],
          composeFileId: file.id,
          isAdmin: widget.isAdmin,
        ),
      ),
    );
  }

  Future<void> _syncFromGit(ComposeFile file) async {
    try {
      final r = await widget.apiClient.syncComposeFile(file.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            r.changed
                ? '${file.name} updated to ${shortCommit(r.commit)} (v${r.file.version}).'
                : '${file.name} is already at the latest commit.',
          ),
        ),
      );
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('Sync failed: ${e is ApiException ? e.message : e}'),
          ),
        );
      }
    }
  }

  Future<void> _openHistory(ComposeFile file) async {
    final changed = await Navigator.of(context).push<bool>(
      MaterialPageRoute(
        builder: (_) => ComposeVersionHistoryScreen(
          apiClient: widget.apiClient,
          composeFile: file,
        ),
      ),
    );
    if (changed == true) _refresh();
  }

  Future<void> _clone(ComposeFile file) async {
    final controller = TextEditingController(text: '${file.name} copy');
    final newName = await showDialog<String>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('Clone Compose file'),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: const InputDecoration(labelText: 'New name'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(controller.text.trim()),
            child: const Text('Clone'),
          ),
        ],
      ),
    );
    controller.dispose();
    if (newName == null || newName.isEmpty || !mounted) return;

    try {
      await widget.apiClient.createComposeFile(
        name: newName,
        content: file.content,
      );
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to clone: $e')));
      }
    }
  }

  Future<void> _export(ComposeFile file) async {
    await showDialog<void>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('${file.name}.yaml'),
        content: SizedBox(
          width: 560,
          height: 480,
          child: SingleChildScrollView(
            child: SelectableText(
              file.content,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () async {
              await Clipboard.setData(ClipboardData(text: file.content));
              if (mounted) {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Copied to clipboard.')),
                );
              }
            },
            child: const Text('Copy to clipboard'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Close'),
          ),
        ],
      ),
    );
  }

  Future<void> _delete(ComposeFile file) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('Delete ${file.name}?'),
        content: const Text(
          'This removes the stored Compose file. This cannot be undone.',
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
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await widget.apiClient.deleteComposeFile(file.id);
      _refresh();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to delete: $e')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final create = PrimaryAction(
      label: 'New Compose file',
      icon: Icons.add,
      onPressed: _openCreate,
    );
    return Scaffold(
      floatingActionButton: create.fab(context),
      body: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          PageIntro(
            description:
                'Compose files you can deploy to any server. Each deploy '
                'shows a preview with pre-deployment checks first.',
            action: create.inline(context),
          ),
          const SizedBox(height: Space.sm),
          Expanded(
            child: FutureBuilder<List<ComposeFile>>(
              future: _filesFuture,
              builder: (context, snapshot) {
                if (snapshot.connectionState == ConnectionState.waiting) {
                  return const Center(child: CircularProgressIndicator());
                }
                if (snapshot.hasError) {
                  return StateMessage.error(
                    what: 'Compose files',
                    error: snapshot.error,
                    onRetry: _refresh,
                  );
                }
                final files = snapshot.data ?? [];
                if (files.isEmpty) {
                  return StateMessage(
                    icon: Icons.layers_outlined,
                    title: 'No Compose files yet',
                    message:
                        'Build one in the visual editor, paste existing YAML, or '
                        'import one from the Git tab.',
                    actionLabel: 'New Compose file',
                    actionIcon: Icons.add,
                    onAction: _openCreate,
                  );
                }
                return ListView(
                  padding: const EdgeInsets.symmetric(vertical: 4),
                  children: [
                    for (final f in files)
                      ListTile(
                        dense: true,
                        leading: Icon(
                          f.isGitLinked
                              ? Icons.source_outlined
                              : Icons.layers_outlined,
                        ),
                        title: Text(f.name),
                        subtitle: Text(
                          [
                            f.serviceNames.isEmpty
                                ? 'No services'
                                : 'Services: ${f.serviceNames.join(', ')}',
                            if (f.isGitLinked)
                              'Git: ${f.gitPath.isEmpty ? 'Generated from Dockerfile' : f.gitPath} @ ${f.gitRef} (${shortCommit(f.gitCommit)})',
                          ].join('\n'),
                        ),
                        trailing: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            IconButton(
                              icon: const Icon(Icons.rocket_launch_outlined),
                              tooltip: 'Deploy',
                              onPressed: () => _deploy(f),
                            ),
                            PopupMenuButton<String>(
                              tooltip: 'More',
                              onSelected: (value) {
                                switch (value) {
                                  case 'history':
                                    _openHistory(f);
                                  case 'sync':
                                    _syncFromGit(f);
                                  case 'clone':
                                    _clone(f);
                                  case 'export':
                                    _export(f);
                                  case 'delete':
                                    _delete(f);
                                }
                              },
                              itemBuilder: (context) => [
                                if (f.isGitLinked)
                                  const PopupMenuItem(
                                    value: 'sync',
                                    child: Text('Sync from Git'),
                                  ),
                                const PopupMenuItem(
                                  value: 'history',
                                  child: Text('Version history'),
                                ),
                                const PopupMenuItem(
                                  value: 'clone',
                                  child: Text('Clone'),
                                ),
                                const PopupMenuItem(
                                  value: 'export',
                                  child: Text('Export / copy YAML'),
                                ),
                                const PopupMenuDivider(),
                                const PopupMenuItem(
                                  value: 'delete',
                                  child: Text('Delete'),
                                ),
                              ],
                            ),
                          ],
                        ),
                        onTap: () => _openEdit(f),
                      ),
                  ],
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}

/// Configuration management: environment variable groups/profiles are
/// implemented below (also covering "secret references" and "reusable
/// configuration templates"); volume/port/restart-policy/resource-limit/
/// health-check mapping live on the Compose visual editor (Compose files
/// tab), and custom networks are declared in the Compose YAML.
class _ConfigurationTab extends StatelessWidget {
  final ApiClient apiClient;

  const _ConfigurationTab({required this.apiClient});

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Icon(
                Icons.info_outline,
                size: 18,
                color: Theme.of(context).colorScheme.outline,
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  'Volume mapping, port mapping, restart policy, CPU/memory '
                  'resource limits, and health checks are edited '
                  'per-service on a Compose file\'s visual editor. Groups '
                  'here are selectable when deploying. Custom networks '
                  '(top-level networks + per-service networks) are declared '
                  'in the Compose YAML and created per deployment.',
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).colorScheme.outline,
                  ),
                ),
              ),
            ],
          ),
        ),
        const Divider(height: 1),
        Expanded(child: EnvVarGroupsScreen(apiClient: apiClient)),
      ],
    );
  }
}

/// What [_EnvVarSetupDialog] pops with: the ad-hoc env vars plus
/// "Development, test, and production environments" + "Change request
/// reference" + "Deployment notes" — governance metadata gathered at the
/// same pre-deploy step, since it's all "fill this in before you deploy".
class _DeploySetup {
  final Map<String, String> env;
  final DeploymentMetadata metadata;

  const _DeploySetup({
    required this.env,
    this.metadata = const DeploymentMetadata(),
  });
}

/// "Environment variable editor" + deployment governance metadata: lets
/// the user optionally load a saved [EnvVarGroup] as a starting point for
/// env vars, then edit ad-hoc, and set an environment/change request/
/// notes before deploying. Skip pops a [_DeploySetup] with empty env
/// rather than null, so the caller can tell "nothing filled in" apart
/// from "dialog dismissed" (null).
class _EnvVarSetupDialog extends StatefulWidget {
  final ApiClient apiClient;
  final ComposeFile file;

  const _EnvVarSetupDialog({required this.apiClient, required this.file});

  @override
  State<_EnvVarSetupDialog> createState() => _EnvVarSetupDialogState();
}

class _EnvVarSetupDialogState extends State<_EnvVarSetupDialog> {
  late final Future<List<EnvVarGroup>> _groupsFuture;
  final _editorKey = GlobalKey<EnvVariableEditorState>();
  final _changeRequestController = TextEditingController();
  final _notesController = TextEditingController();
  final _rollbackPlanController = TextEditingController();
  final _gitRefController = TextEditingController();
  List<EnvVariable> _initialVariables = const [];
  int _editorGeneration = 0;
  String? _environment;
  String _strategy = 'recreate';
  bool _autoRollback = false;
  bool _autoDeploy = false;

  @override
  void initState() {
    super.initState();
    _groupsFuture = widget.apiClient.listEnvVarGroups();
  }

  @override
  void dispose() {
    _changeRequestController.dispose();
    _notesController.dispose();
    _rollbackPlanController.dispose();
    _gitRefController.dispose();
    super.dispose();
  }

  void _loadGroup(EnvVarGroup group) {
    setState(() {
      _initialVariables = group.variables;
      _editorGeneration++;
    });
  }

  _DeploySetup _currentSetup() {
    final variables = _editorKey.currentState?.currentVariables() ?? const [];
    return _DeploySetup(
      env: {for (final v in variables) v.key: v.value},
      metadata: DeploymentMetadata(
        environment: _environment,
        changeRequest: _changeRequestController.text.trim(),
        notes: _notesController.text.trim(),
        rollbackPlan: _rollbackPlanController.text.trim(),
        autoRollback: _autoRollback,
        updateStrategy: _strategy,
        gitRef: _gitRefController.text.trim(),
        autoDeploy: _autoDeploy,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Deployment setup'),
      content: SizedBox(
        width: 460,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              DropdownButtonFormField<String>(
                initialValue: _environment,
                decoration: const InputDecoration(
                  labelText: 'Environment (optional)',
                  isDense: true,
                ),
                items: [
                  for (final env in kEnvironments)
                    DropdownMenuItem(
                      value: env,
                      child: Text(env[0].toUpperCase() + env.substring(1)),
                    ),
                ],
                onChanged: (v) => setState(() => _environment = v),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _changeRequestController,
                decoration: const InputDecoration(
                  labelText: 'Change request reference (optional)',
                  hintText: 'e.g. JIRA-1234',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _notesController,
                maxLines: 2,
                decoration: const InputDecoration(
                  labelText: 'Deployment notes (optional)',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _rollbackPlanController,
                maxLines: 2,
                decoration: const InputDecoration(
                  labelText: 'Rollback plan (optional)',
                  hintText: 'Required by some environments',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: _strategy,
                decoration: const InputDecoration(
                  labelText: 'Update strategy for redeploys',
                  isDense: true,
                ),
                items: const [
                  DropdownMenuItem(
                    value: 'recreate',
                    child: Text('Recreate — replace everything at once'),
                  ),
                  DropdownMenuItem(
                    value: 'rolling',
                    child: Text(
                      'Rolling — one container at a time, auto-rollback',
                    ),
                  ),
                ],
                onChanged: (v) => setState(() => _strategy = v ?? 'recreate'),
              ),
              CheckboxListTile(
                contentPadding: EdgeInsets.zero,
                value: _autoRollback,
                onChanged: (v) => setState(() => _autoRollback = v ?? false),
                title: const Text('Roll back automatically if unhealthy'),
              ),
              if (widget.file.isGitLinked) ...[
                TextField(
                  controller: _gitRefController,
                  decoration: InputDecoration(
                    labelText: 'Git branch or tag to deploy (optional)',
                    hintText: 'Empty = ${widget.file.gitRef}',
                    isDense: true,
                  ),
                ),
                CheckboxListTile(
                  contentPadding: EdgeInsets.zero,
                  value: _autoDeploy,
                  onChanged: (v) => setState(() => _autoDeploy = v ?? false),
                  title: const Text('Redeploy automatically on Git push'),
                ),
              ],
              const SizedBox(height: 16),
              FutureBuilder<List<EnvVarGroup>>(
                future: _groupsFuture,
                builder: (context, snapshot) {
                  final groups = snapshot.data ?? const <EnvVarGroup>[];
                  if (groups.isEmpty) return const SizedBox.shrink();
                  return Padding(
                    padding: const EdgeInsets.only(bottom: 12),
                    child: DropdownButtonFormField<EnvVarGroup>(
                      decoration: const InputDecoration(
                        labelText:
                            'Load env vars from a saved group (optional)',
                        isDense: true,
                      ),
                      items: [
                        for (final g in groups)
                          DropdownMenuItem(
                            value: g,
                            child: Text('${g.name} (${g.environment})'),
                          ),
                      ],
                      onChanged: (g) {
                        if (g != null) _loadGroup(g);
                      },
                    ),
                  );
                },
              ),
              EnvVariableEditor(
                key: ValueKey(_editorGeneration),
                initialVariables: _initialVariables,
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () =>
              Navigator.of(context).pop(const _DeploySetup(env: {})),
          child: const Text('Skip'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(_currentSetup()),
          child: const Text('Continue'),
        ),
      ],
    );
  }
}

/// "Preview the resources before deployment": shows exactly what
/// POST /api/deployments/preview resolved (per-service image, ports,
/// volumes) before the user commits to actually deploying.
class _DeployPreviewDialog extends StatelessWidget {
  final DeploymentPreview preview;
  final String serverName;

  const _DeployPreviewDialog({required this.preview, required this.serverName});

  @override
  Widget build(BuildContext context) {
    final check = preview.resourceCheck;
    return AlertDialog(
      title: Text('Deploy ${preview.name} to $serverName'),
      content: SizedBox(
        width: 420,
        child: preview.services.isEmpty
            ? const Text('No services found in this compose file.')
            : SingleChildScrollView(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    _PreflightChecklist(preview: preview),
                    const SizedBox(height: 12),
                    if (check != null) ...[
                      _ResourceCheckSummary(check: check),
                      const SizedBox(height: 12),
                    ],
                    if (preview.portConflicts.isNotEmpty) ...[
                      _PortConflictSummary(conflicts: preview.portConflicts),
                      const SizedBox(height: 12),
                    ],
                    if (_hasValidationIssues(preview)) ...[
                      _ValidationIssuesSummary(preview: preview),
                      const SizedBox(height: 12),
                    ],
                    const Divider(),
                    const SizedBox(height: 4),
                    for (final svc in preview.services) ...[
                      Text(
                        svc.name,
                        style: Theme.of(context).textTheme.titleSmall,
                      ),
                      Text(
                        svc.image,
                        style: Theme.of(context).textTheme.bodySmall,
                      ),
                      if (svc.ports.isNotEmpty)
                        Text('Ports: ${svc.ports.join(', ')}'),
                      if (svc.volumes.isNotEmpty)
                        Text('Volumes: ${svc.volumes.join(', ')}'),
                      if (svc.environmentCount > 0)
                        Text('${svc.environmentCount} environment variable(s)'),
                      if (svc.nanoCpus > 0 || svc.memoryLimitBytes > 0)
                        Text(
                          [
                            if (svc.nanoCpus > 0)
                              '${(svc.nanoCpus / 1e9).toStringAsFixed(2)} CPU',
                            if (svc.memoryLimitBytes > 0)
                              formatBytes(svc.memoryLimitBytes),
                          ].join(' • '),
                        ),
                      const SizedBox(height: 12),
                    ],
                  ],
                ),
              ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(true),
          child: Text(
            (check != null && check.riskScore == 'high') ||
                    preview.portConflicts.isNotEmpty ||
                    _hasValidationIssues(preview)
                ? 'Deploy anyway'
                : 'Deploy',
          ),
        ),
      ],
    );
  }

  static bool _hasValidationIssues(DeploymentPreview preview) {
    return preview.imageChecks.any((c) => !c.available || !c.archCompatible) ||
        preview.volumeWarnings.isNotEmpty ||
        preview.missingSecrets.isNotEmpty ||
        preview.networkWarnings.isNotEmpty;
  }
}

enum _CheckOutcome { pass, warn, fail, skipped }

/// Every pre-deployment check as one pass/warn/fail/skipped line at the top
/// of the deploy preview, so a clean result is visible too (not just the
/// absence of warnings). Details for anything that didn't pass follow
/// below it in [_ResourceCheckSummary], [_PortConflictSummary] and
/// [_ValidationIssuesSummary].
class _PreflightChecklist extends StatelessWidget {
  final DeploymentPreview preview;

  const _PreflightChecklist({required this.preview});

  List<(String, _CheckOutcome, String)> _checks() {
    final check = preview.resourceCheck;
    final images = preview.imageChecks;
    final unavailable = images.where((c) => !c.available).length;
    final wrongArch = images
        .where((c) => c.available && !c.archCompatible)
        .length;

    return [
      if (check == null)
        (
          'CPU, memory & disk',
          _CheckOutcome.skipped,
          'Server hasn\'t reported its capacity yet',
        )
      else if (!check.sufficientCpu || !check.sufficientMemory)
        ('CPU, memory & disk', _CheckOutcome.fail, 'Not enough capacity')
      else
        (
          'CPU, memory & disk',
          switch (check.riskScore) {
            'high' => _CheckOutcome.fail,
            'medium' => _CheckOutcome.warn,
            _ => _CheckOutcome.pass,
          },
          switch (check.riskScore) {
            'high' => 'High risk',
            'medium' => 'Medium risk',
            _ => 'Low risk',
          },
        ),
      preview.portConflicts.isEmpty
          ? ('Host ports', _CheckOutcome.pass, 'No conflicts')
          : (
              'Host ports',
              _CheckOutcome.fail,
              '${preview.portConflicts.length} already in use',
            ),
      if (images.isEmpty)
        ('Images & architecture', _CheckOutcome.skipped, 'Not checked')
      else if (unavailable > 0)
        (
          'Images & architecture',
          _CheckOutcome.fail,
          '$unavailable unavailable',
        )
      else if (wrongArch > 0)
        (
          'Images & architecture',
          _CheckOutcome.warn,
          '$wrongArch not built for this server',
        )
      else
        ('Images & architecture', _CheckOutcome.pass, 'All available'),
      preview.volumeWarnings.isEmpty
          ? ('Volume paths', _CheckOutcome.pass, 'Valid')
          : (
              'Volume paths',
              _CheckOutcome.warn,
              '${preview.volumeWarnings.length} issue(s)',
            ),
      preview.missingSecrets.isEmpty
          ? ('Secrets & variables', _CheckOutcome.pass, 'All set')
          : (
              'Secrets & variables',
              _CheckOutcome.fail,
              '${preview.missingSecrets.length} missing',
            ),
      preview.networkWarnings.isEmpty
          ? ('Networks', _CheckOutcome.pass, 'Valid')
          : (
              'Networks',
              _CheckOutcome.warn,
              '${preview.networkWarnings.length} issue(s)',
            ),
    ];
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final checks = _checks();
    final failed = checks.where((c) => c.$2 == _CheckOutcome.fail).length;
    final warned = checks.where((c) => c.$2 == _CheckOutcome.warn).length;
    final headline = failed > 0
        ? '$failed check(s) failed'
        : warned > 0
        ? 'Passed with $warned warning(s)'
        : 'All checks passed';

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Pre-deployment checks · $headline',
          style: theme.textTheme.titleSmall,
        ),
        const SizedBox(height: 6),
        for (final (name, outcome, detail) in checks)
          Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: Row(
              children: [
                _outcomeIcon(context, outcome),
                const SizedBox(width: 8),
                Expanded(child: Text(name)),
                const SizedBox(width: 8),
                Flexible(
                  child: Text(
                    detail,
                    textAlign: TextAlign.end,
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                ),
              ],
            ),
          ),
        Text(
          'Service dependencies and conflicting settings were checked when '
          'the Compose file was saved.',
          style: theme.textTheme.bodySmall?.copyWith(
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      ],
    );
  }

  static Widget _outcomeIcon(BuildContext context, _CheckOutcome outcome) {
    return switch (outcome) {
      _CheckOutcome.pass => const Icon(
        Icons.check_circle,
        size: 18,
        color: AppColors.healthy,
      ),
      _CheckOutcome.warn => const Icon(
        Icons.warning_amber_rounded,
        size: 18,
        color: AppColors.warning,
      ),
      _CheckOutcome.fail => Icon(
        Icons.cancel,
        size: 18,
        color: Theme.of(context).colorScheme.error,
      ),
      _CheckOutcome.skipped => Icon(
        Icons.remove_circle_outline,
        size: 18,
        color: Theme.of(context).colorScheme.outline,
      ),
    };
  }
}

/// "Check image availability" + "Check host architecture compatibility" +
/// "Validate volume paths" + "Detect missing secrets" + "Validate network
/// configuration" — grouped into one list since each is a single-line
/// finding rather than something needing its own rich layout.
class _ValidationIssuesSummary extends StatelessWidget {
  final DeploymentPreview preview;

  const _ValidationIssuesSummary({required this.preview});

  @override
  Widget build(BuildContext context) {
    final errorColor = Theme.of(context).colorScheme.error;
    const warnColor = AppColors.warning;

    final lines = <(String, Color)>[
      for (final c in preview.imageChecks)
        if (!c.available)
          (
            '${c.service}: image "${c.image}" unavailable — ${c.error}',
            errorColor,
          )
        else if (!c.archCompatible)
          (
            '${c.service}: image "${c.image}" doesn\'t publish a build for '
                'this server\'s architecture (has: ${c.platforms.join(', ')})',
            warnColor,
          ),
      for (final w in preview.volumeWarnings)
        ('${w.service}: ${w.message} (${w.target})', warnColor),
      for (final s in preview.missingSecrets)
        ('Environment variable "$s" is referenced but not set', errorColor),
      for (final n in preview.networkWarnings) (n, warnColor),
    ];
    if (lines.isEmpty) return const SizedBox.shrink();

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(Icons.fact_check_outlined, size: 16, color: errorColor),
            const SizedBox(width: 6),
            Text(
              'Validation findings',
              style: Theme.of(
                context,
              ).textTheme.titleSmall?.copyWith(color: errorColor),
            ),
          ],
        ),
        const SizedBox(height: 4),
        for (final (text, color) in lines)
          Padding(
            padding: const EdgeInsets.only(bottom: 2),
            child: Text('• $text', style: TextStyle(color: color)),
          ),
      ],
    );
  }
}

/// "Check required ports": lists any published host port that's already
/// bound by another container on the target server.
class _PortConflictSummary extends StatelessWidget {
  final List<DeploymentPortConflict> conflicts;

  const _PortConflictSummary({required this.conflicts});

  @override
  Widget build(BuildContext context) {
    final color = Theme.of(context).colorScheme.error;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(Icons.warning_amber_outlined, size: 16, color: color),
            const SizedBox(width: 6),
            Text(
              'Port conflicts',
              style: Theme.of(
                context,
              ).textTheme.titleSmall?.copyWith(color: color),
            ),
          ],
        ),
        const SizedBox(height: 4),
        for (final c in conflicts)
          Text(
            '${c.service}: host port ${c.hostPort}/${c.protocol} is already '
            'used by container ${c.containerId.substring(0, c.containerId.length < 12 ? c.containerId.length : 12)}',
            style: TextStyle(color: color),
          ),
      ],
    );
  }
}

/// "Confirm sufficient CPU, RAM, and storage" + "Show estimated resource
/// consumption" + "Generate deployment risk score".
class _ResourceCheckSummary extends StatelessWidget {
  final DeploymentResourceCheck check;

  const _ResourceCheckSummary({required this.check});

  @override
  Widget build(BuildContext context) {
    final Color color;
    final String label;
    switch (check.riskScore) {
      case 'high':
        color = Theme.of(context).colorScheme.error;
        label = 'High risk';
      case 'medium':
        color = AppColors.warning;
        label = 'Medium risk';
      default:
        color = AppColors.healthy;
        label = 'Low risk';
    }

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Container(
              width: 8,
              height: 8,
              decoration: BoxDecoration(color: color, shape: BoxShape.circle),
            ),
            const SizedBox(width: 8),
            Text(
              label,
              style: Theme.of(
                context,
              ).textTheme.titleSmall?.copyWith(color: color),
            ),
          ],
        ),
        const SizedBox(height: 4),
        if (check.requestedMemoryBytes > 0)
          Text(
            'Requests ${formatBytes(check.requestedMemoryBytes)} of '
            '${formatBytes(check.serverAvailableMemoryBytes)} available'
            '${check.sufficientMemory ? '' : ' — not enough memory'}',
            style: check.sufficientMemory
                ? null
                : TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        if (check.requestedNanoCpus > 0)
          Text(
            'Requests ${(check.requestedNanoCpus / 1e9).toStringAsFixed(2)} '
            'of ${check.serverAvailableCpuCores.toStringAsFixed(2)} CPU '
            'cores available'
            '${check.sufficientCpu ? '' : ' — not enough CPU'}',
            style: check.sufficientCpu
                ? null
                : TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        if (check.requestedMemoryBytes == 0 && check.requestedNanoCpus == 0)
          Text(
            'No CPU/memory limits declared — showing server capacity only.',
            style: Theme.of(context).textTheme.bodySmall,
          ),
        Text(
          'Server: ${check.serverTotalCpus} CPU(s), '
          '${formatBytes(check.serverTotalMemoryBytes)} RAM, '
          '${check.serverDiskPercentUsed.toStringAsFixed(0)}% disk used',
          style: Theme.of(context).textTheme.bodySmall,
        ),
      ],
    );
  }
}
