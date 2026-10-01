import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import 'dialog_controllers.dart';
import 'helm_releases_screen.dart';

class KubernetesScreen extends StatefulWidget {
  final ApiClient apiClient;
  final bool isAdmin;

  const KubernetesScreen({
    super.key,
    required this.apiClient,
    required this.isAdmin,
  });

  @override
  State<KubernetesScreen> createState() => _KubernetesScreenState();
}

class _KubernetesScreenState extends State<KubernetesScreen> {
  late Future<List<Map<String, dynamic>>> _clusters = widget.apiClient
      .listKubernetesClusters();
  bool _creatingLocal = false;

  void _refresh() => setState(() {
    _clusters = widget.apiClient.listKubernetesClusters();
  });

  Future<void> _add() async {
    final name = TextEditingController();
    final config = TextEditingController();
    final submitted = await showDialogDisposing<bool>(
      context,
      [name, config],
      (context) => AlertDialog(
        title: const Text('Connect Kubernetes cluster'),
        content: SizedBox(
          width: 520,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: name,
                decoration: const InputDecoration(labelText: 'Cluster name'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: config,
                maxLines: 8,
                decoration: const InputDecoration(
                  labelText: 'Kubeconfig YAML',
                  border: OutlineInputBorder(),
                  helperText:
                      'Use inline token or certificate credentials; no file paths or exec plugins.',
                ),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Connect'),
          ),
        ],
      ),
    );
    if (submitted == true) {
      try {
        await widget.apiClient.addKubernetesCluster(
          name.text.trim(),
          config.text,
        );
        _refresh();
      } catch (error) {
        if (mounted) {
          ScaffoldMessenger.of(
            context,
          ).showSnackBar(SnackBar(content: Text('$error')));
        }
      }
    }
  }

  Future<void> _createLocal() async {
    final name = TextEditingController(text: 'Local Kubernetes');
    final submitted = await showDialogDisposing<bool>(
      context,
      [name],
      (context) => AlertDialog(
        title: const Text('Create local Kubernetes cluster'),
        content: SizedBox(
          width: 480,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text(
                'Creates a kind cluster on the control plane machine. That machine needs kind, Docker CLI, and a running Docker Engine. Creation can take several minutes.',
              ),
              TextField(
                controller: name,
                decoration: const InputDecoration(labelText: 'Cluster name'),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Create'),
          ),
        ],
      ),
    );
    if (submitted != true || name.text.trim().isEmpty) return;
    setState(() => _creatingLocal = true);
    try {
      await widget.apiClient.createLocalKubernetesCluster(name.text.trim());
      _refresh();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$error')));
      }
    } finally {
      if (mounted) setState(() => _creatingLocal = false);
    }
  }

  Future<void> _remove(Map<String, dynamic> cluster) async {
    final local = (cluster['localKindName'] as String? ?? '').isNotEmpty;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(local ? 'Delete local cluster?' : 'Disconnect cluster?'),
        content: Text(
          local
              ? 'Delete ${cluster['name']} and all workloads inside it? This removes its kind containers and data.'
              : 'Remove ${cluster['name']} from PSpocketEdge? Kubernetes workloads will keep running.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(local ? 'Delete cluster' : 'Disconnect'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      if (local) {
        await widget.apiClient.deleteLocalKubernetesCluster(
          cluster['id'] as String,
        );
      } else {
        await widget.apiClient.removeKubernetesCluster(cluster['id'] as String);
      }
      _refresh();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$error')));
      }
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text('Kubernetes'),
      actions: [
        IconButton(
          onPressed: _refresh,
          icon: const Icon(Icons.refresh),
          tooltip: 'Refresh',
        ),
        if (_creatingLocal)
          const Padding(
            padding: EdgeInsets.all(12),
            child: SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
          ),
        if (widget.isAdmin)
          PopupMenuButton<String>(
            enabled: !_creatingLocal,
            icon: const Icon(Icons.add),
            tooltip: 'Add cluster',
            onSelected: (value) {
              if (value == 'local') {
                _createLocal();
              } else {
                _add();
              }
            },
            itemBuilder: (_) => const [
              PopupMenuItem(
                value: 'local',
                child: Text('Create local cluster'),
              ),
              PopupMenuItem(
                value: 'connect',
                child: Text('Connect existing cluster'),
              ),
            ],
          ),
      ],
    ),
    body: FutureBuilder<List<Map<String, dynamic>>>(
      future: _clusters,
      builder: (context, snapshot) {
        if (!snapshot.hasData) {
          return Center(
            child: Text(
              snapshot.hasError ? '${snapshot.error}' : 'Loading clusters…',
            ),
          );
        }
        final clusters = snapshot.data!;
        if (clusters.isEmpty) {
          return const Center(child: Text('No Kubernetes clusters connected.'));
        }
        return ListView.builder(
          itemCount: clusters.length,
          itemBuilder: (context, index) {
            final cluster = clusters[index];
            return ListTile(
              leading: const Icon(Icons.hub_outlined),
              title: Text('${cluster['name']}'),
              subtitle: Text(
                (cluster['localKindName'] as String? ?? '').isNotEmpty
                    ? 'Local kind • ${cluster['apiServer']}'
                    : '${cluster['apiServer']}',
              ),
              trailing: widget.isAdmin
                  ? IconButton(
                      onPressed: () => _remove(cluster),
                      icon: const Icon(Icons.delete_outline),
                      tooltip:
                          (cluster['localKindName'] as String? ?? '').isNotEmpty
                          ? 'Delete local cluster'
                          : 'Disconnect',
                    )
                  : null,
              onTap: () => Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => KubernetesClusterScreen(
                    apiClient: widget.apiClient,
                    cluster: cluster,
                    isAdmin: widget.isAdmin,
                  ),
                ),
              ),
            );
          },
        );
      },
    ),
  );
}

class KubernetesClusterScreen extends StatefulWidget {
  final ApiClient apiClient;
  final Map<String, dynamic> cluster;
  final bool isAdmin;

  const KubernetesClusterScreen({
    super.key,
    required this.apiClient,
    required this.cluster,
    required this.isAdmin,
  });

  @override
  State<KubernetesClusterScreen> createState() =>
      _KubernetesClusterScreenState();
}

class _KubernetesClusterScreenState extends State<KubernetesClusterScreen> {
  String? _namespace;
  late Future<Map<String, dynamic>> _overview = _load();
  String get _id => widget.cluster['id'] as String;

  Future<Map<String, dynamic>> _load() =>
      widget.apiClient.kubernetesOverview(_id, namespace: _namespace);
  void _refresh() => setState(() {
    _overview = _load();
  });

  List<Map<String, dynamic>> _items(Map<String, dynamic> data, String key) =>
      ((data[key] as List?) ?? []).cast<Map<String, dynamic>>();

  Future<void> _logs(String namespace, String pod) async {
    try {
      final logs = await widget.apiClient.kubernetesPodLogs(
        _id,
        namespace,
        pod,
      );
      if (!mounted) return;
      await showDialog<void>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text('$pod logs'),
          content: SizedBox(
            width: 700,
            height: 420,
            child: SingleChildScrollView(
              child: SelectableText(logs.isEmpty ? 'No recent logs.' : logs),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('Close'),
            ),
          ],
        ),
      );
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$error')));
      }
    }
  }

  Future<void> _deploy(
    List<String> namespaces,
    List<String> gpuResources, {
    Map<String, dynamic>? existing,
  }) async {
    final name = TextEditingController(
      text: existing?['name'] as String? ?? '',
    );
    final image = TextEditingController(
      text: existing?['image'] as String? ?? '',
    );
    final cpu = TextEditingController(
      text: existing?['cpu'] as String? ?? '250m',
    );
    final memory = TextEditingController(
      text: existing?['memory'] as String? ?? '512Mi',
    );
    final replicas = TextEditingController(
      text: '${existing?['replicas'] ?? 1}',
    );
    final gpuCount = TextEditingController(
      text: '${existing?['gpuCount'] ?? 0}',
    );
    String namespace =
        existing?['namespace'] as String? ??
        _namespace ??
        (namespaces.contains('default') ? 'default' : namespaces.first);
    String? gpuResource = existing?['gpuResource'] as String?;
    if (gpuResource == '') gpuResource = null;
    // Keep the deployment's current values selectable even if the cluster no
    // longer lists them (namespace list truncated, GPU node removed).
    final namespaceOptions = [
      ...namespaces,
      if (!namespaces.contains(namespace)) namespace,
    ];
    final gpuOptions = [
      ...gpuResources,
      if (gpuResource != null && !gpuResources.contains(gpuResource))
        gpuResource,
    ];
    bool busy = false;
    if (!mounted) return;
    await showDialogDisposing<void>(
      context,
      [name, image, cpu, memory, replicas, gpuCount],
      (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) {
          Future<void> submit() async {
            final spec = {
              'namespace': namespace,
              'name': name.text.trim(),
              'image': image.text.trim(),
              'cpu': cpu.text.trim(),
              'memory': memory.text.trim(),
              'replicas': int.tryParse(replicas.text) ?? 0,
              'gpuCount': int.tryParse(gpuCount.text) ?? 0,
              'gpuResource': gpuResource ?? '',
            };
            setDialogState(() => busy = true);
            try {
              Map<String, dynamic> preview;
              if (existing == null) {
                preview = await widget.apiClient.deployKubernetesWorkload(
                  _id,
                  spec,
                  dryRun: true,
                );
                if (!dialogContext.mounted) return;
                final approved = await _confirmWorkload(
                  dialogContext,
                  spec,
                  existing,
                  preview,
                );
                if (approved != true) return;
                await widget.apiClient.deployKubernetesWorkload(_id, spec);
              } else {
                preview = await widget.apiClient.updateKubernetesWorkload(
                  _id,
                  namespace,
                  name.text,
                  spec,
                  dryRun: true,
                );
                if (!dialogContext.mounted) return;
                final approved = await _confirmWorkload(
                  dialogContext,
                  spec,
                  existing,
                  preview,
                );
                if (approved != true) return;
                await widget.apiClient.updateKubernetesWorkload(
                  _id,
                  namespace,
                  name.text,
                  spec,
                );
              }
              if (dialogContext.mounted) Navigator.pop(dialogContext);
              _refresh();
            } catch (error) {
              if (dialogContext.mounted) {
                ScaffoldMessenger.of(
                  dialogContext,
                ).showSnackBar(SnackBar(content: Text('$error')));
              }
            } finally {
              if (dialogContext.mounted) setDialogState(() => busy = false);
            }
          }

          return AlertDialog(
            title: Text(
              existing == null ? 'Deploy to Kubernetes' : 'Update deployment',
            ),
            content: SizedBox(
              width: 480,
              child: SingleChildScrollView(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    DropdownButtonFormField<String>(
                      initialValue: namespace,
                      decoration: const InputDecoration(labelText: 'Namespace'),
                      items: [
                        for (final ns in namespaceOptions)
                          DropdownMenuItem(value: ns, child: Text(ns)),
                      ],
                      onChanged: existing != null
                          ? null
                          : (value) {
                              if (value != null) {
                                setDialogState(() => namespace = value);
                              }
                            },
                    ),
                    TextField(
                      controller: name,
                      readOnly: existing != null,
                      decoration: const InputDecoration(
                        labelText: 'Deployment name',
                      ),
                    ),
                    TextField(
                      controller: image,
                      decoration: const InputDecoration(
                        labelText: 'Container image',
                      ),
                    ),
                    TextField(
                      controller: replicas,
                      keyboardType: TextInputType.number,
                      decoration: const InputDecoration(labelText: 'Replicas'),
                    ),
                    TextField(
                      controller: cpu,
                      decoration: const InputDecoration(
                        labelText: 'CPU request and limit (e.g. 500m)',
                      ),
                    ),
                    TextField(
                      controller: memory,
                      decoration: const InputDecoration(
                        labelText: 'Memory request and limit (e.g. 1Gi)',
                      ),
                    ),
                    if (gpuOptions.isNotEmpty) ...[
                      DropdownButtonFormField<String>(
                        initialValue: gpuResource,
                        decoration: const InputDecoration(
                          labelText: 'GPU resource (optional)',
                        ),
                        items: [
                          const DropdownMenuItem<String>(
                            value: null,
                            child: Text('No GPU'),
                          ),
                          for (final gpu in gpuOptions)
                            DropdownMenuItem(value: gpu, child: Text(gpu)),
                        ],
                        onChanged: (value) =>
                            setDialogState(() => gpuResource = value),
                      ),
                      TextField(
                        controller: gpuCount,
                        keyboardType: TextInputType.number,
                        decoration: const InputDecoration(
                          labelText: 'GPU count',
                        ),
                      ),
                    ],
                    const SizedBox(height: 8),
                    const Text(
                      'The Kubernetes API validates this change in dry-run mode first.',
                    ),
                  ],
                ),
              ),
            ),
            actions: [
              TextButton(
                onPressed: busy ? null : () => Navigator.pop(context),
                child: const Text('Cancel'),
              ),
              FilledButton(
                onPressed: busy ? null : submit,
                child: Text(
                  busy ? 'Saving…' : (existing == null ? 'Deploy' : 'Update'),
                ),
              ),
            ],
          );
        },
      ),
    );
  }

  Future<bool?> _confirmWorkload(
    BuildContext context,
    Map<String, dynamic> spec,
    Map<String, dynamic>? existing,
    Map<String, dynamic> preview,
  ) => showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text(existing == null ? 'Review deployment' : 'Review update'),
      content: Text(
        '${spec['namespace']}/${spec['name']}\n'
        '${existing == null ? '' : 'Current image: ${existing['image']}\n'}'
        'Image: ${spec['image']}\n'
        'Replicas: ${spec['replicas']}\n'
        'CPU: ${spec['cpu']} · Memory: ${spec['memory']}\n'
        'GPU: ${spec['gpuCount']} ${spec['gpuResource']}\n\n'
        'Estimated placement: ${preview['schedule']?['placements'] ?? {}}\n'
        'Kubernetes dry-run validation passed.',
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('Back'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(context, true),
          child: const Text('Apply'),
        ),
      ],
    ),
  );

  Future<void> _history(Map<String, dynamic> item) async {
    final namespace = '${item['namespace']}';
    final name = '${item['name']}';
    try {
      final revisions = await widget.apiClient.kubernetesRevisions(
        _id,
        namespace,
        name,
      );
      if (!mounted) return;
      final revision = await showDialog<int>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text('$name revisions'),
          content: SizedBox(
            width: 420,
            child: revisions.isEmpty
                ? const Text('No retained revisions found.')
                : ListView(
                    shrinkWrap: true,
                    children: [
                      for (final entry in revisions)
                        ListTile(
                          title: Text(
                            'Revision ${entry['revision']} · ${entry['image']}',
                          ),
                          subtitle: Text('${entry['createdAt']}'),
                          trailing: entry['current'] == true
                              ? const Text('Current')
                              : const Icon(Icons.restore),
                          onTap: entry['current'] == true
                              ? null
                              : () => Navigator.pop(
                                  context,
                                  entry['revision'] as int,
                                ),
                        ),
                    ],
                  ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('Close'),
            ),
          ],
        ),
      );
      if (revision == null) return;
      await widget.apiClient.rollbackKubernetesWorkload(
        _id,
        namespace,
        name,
        revision,
        dryRun: true,
      );
      if (!mounted) return;
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Roll back deployment?'),
          content: Text(
            'Restore $namespace/$name to revision $revision? Kubernetes dry-run validation passed.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('Roll back'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
      await widget.apiClient.rollbackKubernetesWorkload(
        _id,
        namespace,
        name,
        revision,
      );
      _refresh();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$error')));
      }
    }
  }

  Future<void> _deleteWorkload(Map<String, dynamic> item) async {
    final namespace = '${item['namespace']}';
    final name = '${item['name']}';
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Delete deployment?'),
        content: Text(
          'Delete $namespace/$name and its Pods? Persistent volumes will be retained.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await widget.apiClient.deleteKubernetesWorkload(_id, namespace, name);
      _refresh();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$error')));
      }
    }
  }

  Future<void> _deployOllama(
    List<String> namespaces,
    List<String> gpuResources,
  ) async {
    final name = TextEditingController(text: 'ollama');
    final model = TextEditingController();
    final cpu = TextEditingController(text: '2');
    final memory = TextEditingController(text: '4Gi');
    final storage = TextEditingController(text: '20');
    final gpuCount = TextEditingController(text: '0');
    String namespace =
        _namespace ??
        (namespaces.contains('default') ? 'default' : namespaces.first);
    String? gpuResource;
    bool busy = false;
    await showDialogDisposing<void>(
      context,
      [name, model, cpu, memory, storage, gpuCount],
      (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) {
          Future<void> submit() async {
            final spec = <String, dynamic>{
              'namespace': namespace,
              'name': name.text.trim(),
              'model': model.text.trim(),
              'cpu': cpu.text.trim(),
              'memory': memory.text.trim(),
              'storageGi': int.tryParse(storage.text) ?? 0,
              'gpuResource': gpuResource ?? '',
              'gpuCount': int.tryParse(gpuCount.text) ?? 0,
            };
            setDialogState(() => busy = true);
            try {
              final preview = await widget.apiClient.deployKubernetesOllama(
                _id,
                spec,
                dryRun: true,
              );
              if (!dialogContext.mounted) return;
              final schedule = preview['schedule'] as Map<String, dynamic>?;
              final approved = await showDialog<bool>(
                context: dialogContext,
                builder: (context) => AlertDialog(
                  title: const Text('Review model service'),
                  content: Text(
                    '${spec['namespace']}/${spec['name']} · ${spec['model']}\n'
                    'CPU ${spec['cpu']} · RAM ${spec['memory']} · Storage ${spec['storageGi']} GiB\n'
                    'GPU ${spec['gpuCount']} ${spec['gpuResource']}\n'
                    'Estimated placement: ${schedule?['placements']}\n\n'
                    'The model will download before the service becomes ready.',
                  ),
                  actions: [
                    TextButton(
                      onPressed: () => Navigator.pop(context, false),
                      child: const Text('Back'),
                    ),
                    FilledButton(
                      onPressed: () => Navigator.pop(context, true),
                      child: const Text('Deploy'),
                    ),
                  ],
                ),
              );
              if (approved != true) return;
              await widget.apiClient.deployKubernetesOllama(_id, spec);
              if (dialogContext.mounted) Navigator.pop(dialogContext);
              _refresh();
            } catch (error) {
              if (dialogContext.mounted) {
                ScaffoldMessenger.of(
                  dialogContext,
                ).showSnackBar(SnackBar(content: Text('$error')));
              }
            } finally {
              if (dialogContext.mounted) setDialogState(() => busy = false);
            }
          }

          return AlertDialog(
            title: const Text('Deploy Ollama model'),
            content: SizedBox(
              width: 480,
              child: SingleChildScrollView(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    DropdownButtonFormField<String>(
                      initialValue: namespace,
                      decoration: const InputDecoration(labelText: 'Namespace'),
                      items: [
                        for (final ns in namespaces)
                          DropdownMenuItem(value: ns, child: Text(ns)),
                      ],
                      onChanged: (value) {
                        if (value != null) {
                          setDialogState(() => namespace = value);
                        }
                      },
                    ),
                    TextField(
                      controller: name,
                      decoration: const InputDecoration(
                        labelText: 'Service name',
                      ),
                    ),
                    TextField(
                      controller: model,
                      decoration: const InputDecoration(
                        labelText: 'Ollama model (e.g. llama3.2:1b)',
                      ),
                    ),
                    TextField(
                      controller: cpu,
                      decoration: const InputDecoration(
                        labelText: 'CPU request',
                      ),
                    ),
                    TextField(
                      controller: memory,
                      decoration: const InputDecoration(
                        labelText: 'RAM request',
                      ),
                    ),
                    TextField(
                      controller: storage,
                      keyboardType: TextInputType.number,
                      decoration: const InputDecoration(
                        labelText: 'Model storage (GiB)',
                      ),
                    ),
                    if (gpuResources.isNotEmpty) ...[
                      DropdownButtonFormField<String>(
                        initialValue: gpuResource,
                        decoration: const InputDecoration(
                          labelText: 'GPU resource (optional)',
                        ),
                        items: [
                          const DropdownMenuItem<String>(
                            value: null,
                            child: Text('CPU only'),
                          ),
                          for (final gpu in gpuResources)
                            DropdownMenuItem(value: gpu, child: Text(gpu)),
                        ],
                        onChanged: (value) =>
                            setDialogState(() => gpuResource = value),
                      ),
                      TextField(
                        controller: gpuCount,
                        keyboardType: TextInputType.number,
                        decoration: const InputDecoration(
                          labelText: 'GPU count',
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            ),
            actions: [
              TextButton(
                onPressed: busy ? null : () => Navigator.pop(context),
                child: const Text('Cancel'),
              ),
              FilledButton(
                onPressed: busy ? null : submit,
                child: Text(busy ? 'Checking…' : 'Review'),
              ),
            ],
          );
        },
      ),
    );
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text('${widget.cluster['name']}'),
      actions: [
        IconButton(
          onPressed: _refresh,
          icon: const Icon(Icons.refresh),
          tooltip: 'Refresh',
        ),
      ],
    ),
    body: FutureBuilder<Map<String, dynamic>>(
      future: _overview,
      builder: (context, snapshot) {
        if (!snapshot.hasData) {
          return Center(
            child: Text(
              snapshot.hasError ? '${snapshot.error}' : 'Loading cluster…',
            ),
          );
        }
        final data = snapshot.data!;
        final namespaces = ((data['namespaces'] as List?) ?? []).cast<String>();
        // A filtered namespace may have been deleted since it was chosen.
        final filterOptions = [
          ...namespaces,
          if (_namespace != null && !namespaces.contains(_namespace))
            _namespace!,
        ];
        final nodes = _items(data, 'nodes');
        final deployments = _items(data, 'deployments');
        final pods = _items(data, 'pods');
        final events = _items(data, 'events');
        final gpuResources =
            nodes
                .expand(
                  (node) => ((node['gpu'] as Map?) ?? {}).keys.cast<String>(),
                )
                .toSet()
                .toList()
              ..sort();
        return ListView(
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Wrap(
                spacing: 12,
                runSpacing: 8,
                children: [
                  DropdownButton<String?>(
                    value: _namespace,
                    hint: const Text('All namespaces'),
                    items: [
                      const DropdownMenuItem<String?>(
                        value: null,
                        child: Text('All namespaces'),
                      ),
                      for (final ns in filterOptions)
                        DropdownMenuItem(value: ns, child: Text(ns)),
                    ],
                    onChanged: (value) => setState(() {
                      _namespace = value;
                      _overview = _load();
                    }),
                  ),
                  if (widget.isAdmin && namespaces.isNotEmpty)
                    FilledButton.icon(
                      onPressed: () => _deploy(namespaces, gpuResources),
                      icon: const Icon(Icons.add),
                      label: const Text('Deploy workload'),
                    ),
                  if (widget.isAdmin && namespaces.isNotEmpty)
                    OutlinedButton.icon(
                      onPressed: () => _deployOllama(namespaces, gpuResources),
                      icon: const Icon(Icons.psychology_outlined),
                      label: const Text('Deploy Ollama'),
                    ),
                  if (namespaces.isNotEmpty)
                    OutlinedButton.icon(
                      onPressed: () => Navigator.push(
                        context,
                        MaterialPageRoute(
                          builder: (_) => HelmReleasesScreen(
                            apiClient: widget.apiClient,
                            clusterId: _id,
                            clusterName: '${widget.cluster['name']}',
                            namespaces: namespaces,
                            initialNamespace:
                                _namespace ??
                                (namespaces.contains('default')
                                    ? 'default'
                                    : namespaces.first),
                            isAdmin: widget.isAdmin,
                          ),
                        ),
                      ),
                      icon: const Icon(Icons.layers_outlined),
                      label: const Text('Helm releases'),
                    ),
                  if (data['truncated'] == true)
                    const Text('Showing the first 500 objects per kind.'),
                ],
              ),
            ),
            _section(
              'Nodes',
              nodes
                  .map(
                    (node) => ListTile(
                      leading: Icon(
                        node['ready'] == true
                            ? Icons.check_circle_outline
                            : Icons.warning_amber_outlined,
                      ),
                      title: Text('${node['name']}'),
                      subtitle: Text(
                        'Capacity: CPU ${node['cpu']} · Memory ${node['memory']} · GPU ${node['gpu']}'
                        '${node['usage'] == null ? '' : '\nUsage: ${node['usage']}'}',
                      ),
                    ),
                  )
                  .toList(),
            ),
            _section(
              'Deployments',
              deployments
                  .map(
                    (item) => ListTile(
                      title: Text('${item['namespace']}/${item['name']}'),
                      subtitle: Text(
                        '${item['image']} · ${item['ready']}/${item['replicas']} ready · ${item['status']}',
                      ),
                      trailing: widget.isAdmin && item['managed'] == true
                          ? PopupMenuButton<String>(
                              tooltip: 'Deployment actions',
                              onSelected: (action) {
                                if (action == 'edit') {
                                  _deploy(
                                    namespaces,
                                    gpuResources,
                                    existing: item,
                                  );
                                }
                                if (action == 'history') _history(item);
                                if (action == 'delete') _deleteWorkload(item);
                              },
                              itemBuilder: (_) => const [
                                PopupMenuItem(
                                  value: 'edit',
                                  child: Text('Update'),
                                ),
                                PopupMenuItem(
                                  value: 'history',
                                  child: Text('History and rollback'),
                                ),
                                PopupMenuItem(
                                  value: 'delete',
                                  child: Text('Delete'),
                                ),
                              ],
                            )
                          : null,
                    ),
                  )
                  .toList(),
            ),
            _section(
              'Pods',
              pods
                  .map(
                    (item) => ListTile(
                      title: Text('${item['namespace']}/${item['name']}'),
                      subtitle: Text(
                        '${item['phase']}${item['reason'] == '' ? '' : ' · ${item['reason']}'} · ${item['node']}',
                      ),
                      trailing: IconButton(
                        icon: const Icon(Icons.article_outlined),
                        tooltip: 'View logs',
                        onPressed: () =>
                            _logs('${item['namespace']}', '${item['name']}'),
                      ),
                    ),
                  )
                  .toList(),
            ),
            _section(
              'Events',
              events
                  .map(
                    (item) => ListTile(
                      title: Text('${item['reason']} · ${item['object']}'),
                      subtitle: Text(
                        '${item['namespace']}: ${item['message']}',
                      ),
                    ),
                  )
                  .toList(),
            ),
          ],
        );
      },
    ),
  );

  Widget _section(String title, List<Widget> children) => ExpansionTile(
    title: Text('$title (${children.length})'),
    initiallyExpanded: title == 'Deployments' || title == 'Pods',
    children: children.isEmpty
        ? [const ListTile(title: Text('None found'))]
        : children,
  );
}
