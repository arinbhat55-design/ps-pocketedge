import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../models/compose_file.dart';
import '../../models/deployment_preview.dart';
import '../../models/env_var_group.dart';
import '../../models/server.dart';
import '../deployments/deployment_status_screen.dart';
import 'compose_editor_screen.dart';
import 'compose_version_history_screen.dart';
import 'env_var_groups_screen.dart';
import 'env_variable_editor.dart';

/// Deployment Management > Docker Compose. Tabs mirror the feature areas
/// from the Compose deployment spec; "Compose files" (upload/create,
/// visual + YAML editing, validation, deploy-to-server) is implemented —
/// the rest are placeholders describing what's planned.
class DockerComposeScreen extends StatefulWidget {
  final ApiClient apiClient;

  const DockerComposeScreen({super.key, required this.apiClient});

  @override
  State<DockerComposeScreen> createState() => _DockerComposeScreenState();
}

class _DockerComposeScreenState extends State<DockerComposeScreen>
    with SingleTickerProviderStateMixin {
  static const _tabs = [
    'Compose files',
    'Stack deployment',
    'Configuration',
    'Git-based deployment',
    'Pre-deployment validation',
    'Governance',
  ];

  late final TabController _tabController;

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
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
          child: Text(
            'Docker Compose Deployment Management',
            style: Theme.of(context).textTheme.titleMedium,
          ),
        ),
        TabBar(
          controller: _tabController,
          isScrollable: true,
          tabAlignment: TabAlignment.start,
          tabs: [for (final t in _tabs) Tab(text: t)],
        ),
        const Divider(height: 1),
        Expanded(
          child: TabBarView(
            controller: _tabController,
            children: [
              _ComposeFilesTab(apiClient: widget.apiClient),
              const _PlaceholderTab(
                summary:
                    'Deploy with a resource preview (rocket icon on a '
                    'Compose file), live phase progress, failure reasons, '
                    'full or per-service redeploy, rollback to a past '
                    'Compose file version, start/stop/restart/remove (gear '
                    'icon on the deployment status screen), and automatic '
                    'cleanup of a failed deploy\'s partial containers are '
                    'all done. The rest of stack-level lifecycle '
                    'management is planned.',
                items: [
                  'Show progress broken out per individual service',
                  'Scale supported services',
                ],
              ),
              _ConfigurationTab(apiClient: widget.apiClient),
              const _PlaceholderTab(
                summary: 'Deploy Compose files straight from a Git repository.',
                items: [
                  'Import Compose files from Git',
                  'Connect to GitHub, GitLab, Azure DevOps, or Bitbucket',
                  'Deploy from a selected repository branch or tag',
                  'Use webhooks for automatic redeployment',
                  'Display commit ID associated with each deployment',
                  'Roll back to a previous commit',
                  'Require approval before production deployment',
                  'Detect configuration drift',
                ],
              ),
              const _PlaceholderTab(
                summary: 'Catch problems before a deployment starts.',
                items: [
                  'Confirm sufficient CPU, RAM, and storage',
                  'Check required ports',
                  'Check image availability',
                  'Validate volume paths',
                  'Detect missing secrets',
                  'Validate network configuration',
                  'Check host architecture compatibility',
                  'Show estimated resource consumption',
                  'Generate deployment risk score',
                ],
              ),
              const _PlaceholderTab(
                summary:
                    'Approval, audit, and rollback controls for promoting '
                    'deployments across environments.',
                items: [
                  'Development, test, and production environments',
                  'Approval workflow',
                  'Change request reference',
                  'Maintenance window',
                  'Deployment notes',
                  'Deployment history',
                  'Complete audit trail',
                  'Rollback plan',
                  'Post-deployment health verification',
                ],
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _ComposeFilesTab extends StatefulWidget {
  final ApiClient apiClient;

  const _ComposeFilesTab({required this.apiClient});

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
    setState(() => _filesFuture = widget.apiClient.listComposeFiles());
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
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Failed to load servers: $e')));
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
                    color: s.status == 'online' ? Colors.green : null,
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

    final env = await showDialog<Map<String, String>>(
      context: context,
      builder: (_) => _EnvVarSetupDialog(apiClient: widget.apiClient),
    );
    if (env == null || !mounted) return;

    DeploymentPreview preview;
    try {
      preview = await widget.apiClient.previewDeployment(
        composeFileId: file.id,
        env: env,
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed to preview deployment: $e')));
      }
      return;
    }
    if (!mounted) return;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => _DeployPreviewDialog(
        preview: preview,
        serverName: server.name,
      ),
    );
    if (confirmed != true || !mounted) return;

    try {
      final deploymentId = await widget.apiClient.createComposeDeployment(
        composeFileId: file.id,
        serverId: server.id,
        env: env,
      );
      if (mounted) {
        Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => DeploymentStatusScreen(
              apiClient: widget.apiClient,
              deploymentId: deploymentId,
              serviceNames: [for (final svc in preview.services) svc.name],
              composeFileId: file.id,
            ),
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Failed to start deployment: $e')));
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
              if (context.mounted) {
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
        content: const Text('This removes the stored Compose file. This cannot be undone.'),
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
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Failed to delete: $e')));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _openCreate,
        icon: const Icon(Icons.add),
        label: const Text('New Compose file'),
      ),
      body: FutureBuilder<List<ComposeFile>>(
        future: _filesFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(
              child: Text('Failed to load compose files: ${snapshot.error}'),
            );
          }
          final files = snapshot.data ?? [];
          if (files.isEmpty) {
            return const Center(
              child: Text(
                'No Compose files yet.\nCreate one visually or paste in existing YAML.',
                textAlign: TextAlign.center,
              ),
            );
          }
          return ListView(
            padding: const EdgeInsets.symmetric(vertical: 4),
            children: [
              for (final f in files)
                ListTile(
                  dense: true,
                  leading: const Icon(Icons.layers_outlined),
                  title: Text(f.name),
                  subtitle: Text(
                    f.serviceNames.isEmpty
                        ? 'No services'
                        : 'Services: ${f.serviceNames.join(', ')}',
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
                            case 'clone':
                              _clone(f);
                            case 'export':
                              _export(f);
                            case 'delete':
                              _delete(f);
                          }
                        },
                        itemBuilder: (context) => const [
                          PopupMenuItem(
                            value: 'history',
                            child: Text('Version history'),
                          ),
                          PopupMenuItem(value: 'clone', child: Text('Clone')),
                          PopupMenuItem(
                            value: 'export',
                            child: Text('Export / copy YAML'),
                          ),
                          PopupMenuDivider(),
                          PopupMenuItem(
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
    );
  }
}

/// Configuration management: environment variable groups/profiles are
/// implemented below (also covering "secret references" and "reusable
/// configuration templates"); volume/port/restart-policy mapping already
/// live on the Compose visual editor (Compose files tab). Resource-limit
/// and health-check configuration, and network configuration beyond the
/// automatic per-deployment network, remain planned.
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
                  'here are selectable when deploying. Custom network '
                  'config is still planned.',
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

class _PlaceholderTab extends StatelessWidget {
  final String summary;
  final List<String> items;

  const _PlaceholderTab({required this.summary, required this.items});

  @override
  Widget build(BuildContext context) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Row(
          children: [
            Icon(
              Icons.construction_outlined,
              size: 18,
              color: Theme.of(context).colorScheme.outline,
            ),
            const SizedBox(width: 8),
            Text(
              'Planned — not yet available',
              style: Theme.of(context).textTheme.titleSmall?.copyWith(
                color: Theme.of(context).colorScheme.outline,
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),
        Text(summary, style: Theme.of(context).textTheme.bodyMedium),
        const SizedBox(height: 12),
        for (final item in items)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 3),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text('•  '),
                Expanded(child: Text(item)),
              ],
            ),
          ),
      ],
    );
  }
}

/// "Environment variable editor": lets the user optionally load a saved
/// [EnvVarGroup] as a starting point, then edit ad-hoc before deploying.
/// Pops with the resulting {key: value} map — Skip pops with an empty map
/// rather than null, so the caller can tell "no variables" apart from
/// "dialog dismissed" (null).
class _EnvVarSetupDialog extends StatefulWidget {
  final ApiClient apiClient;

  const _EnvVarSetupDialog({required this.apiClient});

  @override
  State<_EnvVarSetupDialog> createState() => _EnvVarSetupDialogState();
}

class _EnvVarSetupDialogState extends State<_EnvVarSetupDialog> {
  late final Future<List<EnvVarGroup>> _groupsFuture;
  final _editorKey = GlobalKey<EnvVariableEditorState>();
  List<EnvVariable> _initialVariables = const [];
  int _editorGeneration = 0;

  @override
  void initState() {
    super.initState();
    _groupsFuture = widget.apiClient.listEnvVarGroups();
  }

  void _loadGroup(EnvVarGroup group) {
    setState(() {
      _initialVariables = group.variables;
      _editorGeneration++;
    });
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Environment variables'),
      content: SizedBox(
        width: 460,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              FutureBuilder<List<EnvVarGroup>>(
                future: _groupsFuture,
                builder: (context, snapshot) {
                  final groups = snapshot.data ?? const <EnvVarGroup>[];
                  if (groups.isEmpty) return const SizedBox.shrink();
                  return Padding(
                    padding: const EdgeInsets.only(bottom: 12),
                    child: DropdownButtonFormField<EnvVarGroup>(
                      decoration: const InputDecoration(
                        labelText: 'Load from a saved group (optional)',
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
          onPressed: () => Navigator.of(context).pop(<String, String>{}),
          child: const Text('Skip'),
        ),
        FilledButton(
          onPressed: () {
            final variables = _editorKey.currentState?.currentVariables() ?? const [];
            Navigator.of(
              context,
            ).pop({for (final v in variables) v.key: v.value});
          },
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
          child: const Text('Deploy'),
        ),
      ],
    );
  }
}
