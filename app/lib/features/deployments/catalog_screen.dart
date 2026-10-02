import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/stack.dart';

const _categoryLabels = {
  'web': 'Web / Apps',
  'database': 'Databases',
  'ai': 'AI',
  'other': 'Other',
};

IconData _categoryIcon(String category) {
  switch (category) {
    case 'database':
      return Icons.storage;
    case 'ai':
      return Icons.psychology;
    case 'web':
      return Icons.public;
    default:
      return Icons.widgets;
  }
}

/// Browsable catalog of deployable stacks for [serverName]/[serverId],
/// filterable by category. Picking one opens a parameter form (if it has
/// any) before deploying. Pops with the new deployment's ID on success, or
/// null if the user backs out without deploying.
class CatalogScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String serverName;

  const CatalogScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.serverName,
  });

  @override
  State<CatalogScreen> createState() => _CatalogScreenState();
}

class _CatalogScreenState extends State<CatalogScreen> {
  late Future<List<StackSummary>> _stacksFuture;
  String? _selectedCategory;

  @override
  void initState() {
    super.initState();
    _stacksFuture = widget.apiClient.listStacks();
  }

  Future<void> _deployStack(StackSummary stack) async {
    final env = await showModalBottomSheet<Map<String, String>>(
      context: context,
      isScrollControlled: true,
      builder: (_) => _DeployParamsSheet(stack: stack),
    );
    // A parameter sheet with no parameters returns {} immediately without
    // user interaction; null means the sheet was dismissed/cancelled.
    if (env == null || !mounted) return;

    try {
      final deploymentId = await widget.apiClient.createDeployment(
        stackId: stack.id,
        serverId: widget.serverId,
        env: env,
      );
      if (mounted) Navigator.of(context).pop(deploymentId);
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to start deployment: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text('Deploy to ${widget.serverName}')),
      body: FutureBuilder<List<StackSummary>>(
        future: _stacksFuture,
        builder: (context, snapshot) {
          if (snapshot.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snapshot.hasError) {
            return Center(
              child: Text('Failed to load catalog: ${snapshot.error}'),
            );
          }
          final stacks = snapshot.data ?? [];
          final categories = stacks.map((s) => s.category).toSet().toList()
            ..sort();
          final visible = _selectedCategory == null
              ? stacks
              : stacks.where((s) => s.category == _selectedCategory).toList();

          return Column(
            children: [
              if (categories.length > 1)
                Padding(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 8,
                  ),
                  child: Wrap(
                    spacing: 8,
                    children: [
                      ChoiceChip(
                        label: const Text('All'),
                        selected: _selectedCategory == null,
                        onSelected: (_) =>
                            setState(() => _selectedCategory = null),
                      ),
                      for (final c in categories)
                        ChoiceChip(
                          label: Text(_categoryLabels[c] ?? c),
                          selected: _selectedCategory == c,
                          onSelected: (_) =>
                              setState(() => _selectedCategory = c),
                        ),
                    ],
                  ),
                ),
              Expanded(
                child: ListView.builder(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 4,
                  ),
                  itemCount: visible.length,
                  itemBuilder: (context, index) {
                    final stack = visible[index];
                    return Card(
                      margin: const EdgeInsets.symmetric(vertical: 6),
                      child: ListTile(
                        leading: Icon(_categoryIcon(stack.category)),
                        title: Text(stack.name),
                        subtitle: Text(stack.description),
                        isThreeLine: stack.description.length > 40,
                        trailing: FilledButton(
                          onPressed: () => _deployStack(stack),
                          child: const Text('Deploy'),
                        ),
                      ),
                    );
                  },
                ),
              ),
            ],
          );
        },
      ),
    );
  }
}

class _DeployParamsSheet extends StatefulWidget {
  final StackSummary stack;

  const _DeployParamsSheet({required this.stack});

  @override
  State<_DeployParamsSheet> createState() => _DeployParamsSheetState();
}

class _DeployParamsSheetState extends State<_DeployParamsSheet> {
  final _formKey = GlobalKey<FormState>();
  late final Map<String, TextEditingController> _controllers = {
    for (final p in widget.stack.parameters)
      p.key: TextEditingController(text: p.defaultValue),
  };

  @override
  void dispose() {
    for (final c in _controllers.values) {
      c.dispose();
    }
    super.dispose();
  }

  void _submit() {
    if (widget.stack.parameters.isNotEmpty &&
        !_formKey.currentState!.validate()) {
      return;
    }
    final env = {
      for (final entry in _controllers.entries) entry.key: entry.value.text,
    };
    Navigator.of(context).pop(env);
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
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              'Deploy ${widget.stack.name}',
              style: Theme.of(context).textTheme.titleLarge,
            ),
            const SizedBox(height: 4),
            Text(
              widget.stack.description,
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 16),
            for (final p in widget.stack.parameters) ...[
              TextFormField(
                controller: _controllers[p.key],
                obscureText: p.secret,
                decoration: InputDecoration(labelText: p.label),
                validator: (v) => (v == null || v.isEmpty) ? 'Required' : null,
              ),
              const SizedBox(height: 12),
            ],
            FilledButton(onPressed: _submit, child: const Text('Deploy')),
          ],
        ),
      ),
    );
  }
}
