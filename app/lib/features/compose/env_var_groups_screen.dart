import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/env_var_group.dart';
import 'env_variable_editor.dart';

const _environmentLabels = {
  'development': 'Development',
  'test': 'Test',
  'staging': 'Staging',
  'production': 'Production',
};

Color _environmentColor(BuildContext context, String environment) {
  final scheme = Theme.of(context).colorScheme;
  switch (environment) {
    case 'production':
      return scheme.error;
    case 'staging':
      return Colors.orange;
    case 'test':
      return Colors.blue;
    default:
      return scheme.outline;
  }
}

/// Configuration management > environment variable groups: reusable,
/// environment-tagged sets of env vars ("Environment-specific variable
/// groups" + "Development, test, staging, and production profiles" +
/// "Reusable configuration templates" + "Secret references").
class EnvVarGroupsScreen extends StatefulWidget {
  final ApiClient apiClient;

  const EnvVarGroupsScreen({super.key, required this.apiClient});

  @override
  State<EnvVarGroupsScreen> createState() => _EnvVarGroupsScreenState();
}

class _EnvVarGroupsScreenState extends State<EnvVarGroupsScreen> {
  late Future<List<EnvVarGroup>> _groupsFuture;
  String? _filterEnvironment;

  @override
  void initState() {
    super.initState();
    _groupsFuture = widget.apiClient.listEnvVarGroups();
  }

  void _refresh() {
    setState(() => _groupsFuture = widget.apiClient.listEnvVarGroups());
  }

  Future<void> _openCreate() async {
    final created = await showDialog<bool>(
      context: context,
      builder: (_) => _EnvVarGroupDialog(apiClient: widget.apiClient),
    );
    if (created == true) _refresh();
  }

  Future<void> _openEdit(EnvVarGroup group) async {
    final changed = await showDialog<bool>(
      context: context,
      builder: (_) =>
          _EnvVarGroupDialog(apiClient: widget.apiClient, existing: group),
    );
    if (changed == true) _refresh();
  }

  Future<void> _delete(EnvVarGroup group) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('Delete "${group.name}"?'),
        content: const Text('This cannot be undone.'),
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
      await widget.apiClient.deleteEnvVarGroup(group.id);
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
    return Scaffold(
      floatingActionButton: FloatingActionButton.extended(
        // Shares Deployment Management's TabBarView with other tabs' buttons.
        heroTag: 'env-var-groups-add',
        onPressed: _openCreate,
        icon: const Icon(Icons.add),
        label: const Text('New variable group'),
      ),
      body: FutureBuilder<List<EnvVarGroup>>(
        future: _groupsFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(child: Text('Failed to load: ${snapshot.error}'));
          }
          final all = snapshot.data ?? [];
          final groups = _filterEnvironment == null
              ? all
              : all.where((g) => g.environment == _filterEnvironment).toList();

          return Column(
            children: [
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                child: Wrap(
                  spacing: 8,
                  children: [
                    ChoiceChip(
                      label: const Text('All'),
                      selected: _filterEnvironment == null,
                      onSelected: (_) =>
                          setState(() => _filterEnvironment = null),
                    ),
                    for (final env in kEnvironments)
                      ChoiceChip(
                        label: Text(_environmentLabels[env] ?? env),
                        selected: _filterEnvironment == env,
                        onSelected: (_) =>
                            setState(() => _filterEnvironment = env),
                      ),
                  ],
                ),
              ),
              Expanded(
                child: groups.isEmpty
                    ? const Center(
                        child: Text(
                          'No environment variable groups yet.',
                          textAlign: TextAlign.center,
                        ),
                      )
                    : ListView(
                        padding: const EdgeInsets.symmetric(vertical: 4),
                        children: [
                          for (final g in groups)
                            ListTile(
                              dense: true,
                              leading: CircleAvatar(
                                radius: 6,
                                backgroundColor: _environmentColor(
                                  context,
                                  g.environment,
                                ),
                              ),
                              title: Text(g.name),
                              subtitle: Text(
                                '${_environmentLabels[g.environment] ?? g.environment} • '
                                '${g.variables.length} variable(s)',
                              ),
                              trailing: IconButton(
                                icon: const Icon(Icons.delete_outline),
                                tooltip: 'Delete',
                                onPressed: () => _delete(g),
                              ),
                              onTap: () => _openEdit(g),
                            ),
                        ],
                      ),
              ),
            ],
          );
        },
      ),
    );
  }
}

class _EnvVarGroupDialog extends StatefulWidget {
  final ApiClient apiClient;
  final EnvVarGroup? existing;

  const _EnvVarGroupDialog({required this.apiClient, this.existing});

  @override
  State<_EnvVarGroupDialog> createState() => _EnvVarGroupDialogState();
}

class _EnvVarGroupDialogState extends State<_EnvVarGroupDialog> {
  late final TextEditingController _nameController;
  late String _environment;
  final _editorKey = GlobalKey<EnvVariableEditorState>();
  bool _saving = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: widget.existing?.name ?? '');
    _environment = widget.existing?.environment ?? 'development';
  }

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final name = _nameController.text.trim();
    if (name.isEmpty) {
      setState(() => _error = 'Name is required.');
      return;
    }
    final variables = _editorKey.currentState?.currentVariables() ?? const [];

    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      if (widget.existing == null) {
        await widget.apiClient.createEnvVarGroup(
          name: name,
          environment: _environment,
          variables: variables,
        );
      } else {
        await widget.apiClient.updateEnvVarGroup(
          widget.existing!.id,
          name: name,
          environment: _environment,
          variables: variables,
        );
      }
      if (mounted) Navigator.of(context).pop(true);
    } catch (e) {
      setState(() => _error = 'Failed to save: $e');
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(widget.existing == null ? 'New variable group' : 'Edit variable group'),
      content: SizedBox(
        width: 460,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TextField(
                controller: _nameController,
                decoration: const InputDecoration(
                  labelText: 'Name',
                  hintText: 'e.g. Production database',
                  isDense: true,
                ),
              ),
              const SizedBox(height: 12),
              DropdownButtonFormField<String>(
                initialValue: _environment,
                decoration: const InputDecoration(
                  labelText: 'Environment',
                  isDense: true,
                ),
                items: [
                  for (final env in kEnvironments)
                    DropdownMenuItem(
                      value: env,
                      child: Text(_environmentLabels[env] ?? env),
                    ),
                ],
                onChanged: (v) => setState(() => _environment = v ?? _environment),
              ),
              const SizedBox(height: 16),
              EnvVariableEditor(
                key: _editorKey,
                initialVariables: widget.existing?.variables ?? const [],
              ),
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(_error!, style: const TextStyle(color: Colors.red)),
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
