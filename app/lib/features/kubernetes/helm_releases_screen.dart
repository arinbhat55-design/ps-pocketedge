import 'dart:convert';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import 'dialog_controllers.dart';

class HelmReleasesScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String clusterId;
  final String clusterName;
  final List<String> namespaces;
  final String initialNamespace;
  final bool isAdmin;

  const HelmReleasesScreen({
    super.key,
    required this.apiClient,
    required this.clusterId,
    required this.clusterName,
    required this.namespaces,
    required this.initialNamespace,
    required this.isAdmin,
  });

  @override
  State<HelmReleasesScreen> createState() => _HelmReleasesScreenState();
}

class _HelmReleasesScreenState extends State<HelmReleasesScreen> {
  late String _namespace = widget.initialNamespace;
  late Future<List<Map<String, dynamic>>> _releases = _load();

  Future<List<Map<String, dynamic>>> _load() =>
      widget.apiClient.kubernetesHelmReleases(widget.clusterId, _namespace);
  void _refresh() => setState(() {
    _releases = _load();
  });
  void _showError(Object error) {
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('$error')));
    }
  }

  Future<void> _apply() async {
    final name = TextEditingController();
    final values = TextEditingController(text: '{}');
    PlatformFile? chart;
    bool busy = false;
    await showDialogDisposing<void>(
      context,
      [name, values],
      (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) {
          Future<void> pickChart() async {
            try {
              final selected = await FilePicker.pickFile(
                type: FileType.custom,
                allowedExtensions: ['tgz'],
              );
              if (selected != null) setDialogState(() => chart = selected);
            } catch (error) {
              _showError(error);
            }
          }

          Future<void> submit() async {
            if (chart == null) {
              _showError('Select a .tgz Helm chart first.');
              return;
            }
            Object? parsed;
            try {
              parsed = jsonDecode(values.text);
            } catch (_) {
              _showError('Values must be valid JSON.');
              return;
            }
            if (parsed is! Map<String, dynamic>) {
              _showError('Values must be a JSON object.');
              return;
            }
            final length = await chart!.length();
            if (length == null || length > 8 * 1024 * 1024) {
              _showError('Chart must be 8 MB or smaller.');
              return;
            }
            setDialogState(() => busy = true);
            try {
              final archive = await chart!.readAsBytes();
              final spec = {
                'namespace': _namespace,
                'name': name.text.trim(),
                'chartBase64': base64Encode(archive),
                'values': parsed,
              };
              final preview = await widget.apiClient.applyKubernetesHelmChart(
                widget.clusterId,
                spec,
                dryRun: true,
              );
              if (!dialogContext.mounted) return;
              final confirmed = await showDialog<bool>(
                context: dialogContext,
                builder: (context) => AlertDialog(
                  title: const Text('Review Helm release'),
                  content: Text(
                    '${preview['name']} · ${preview['chart']} ${preview['chartVersion']}\n'
                    'Namespace: $_namespace\nChart: ${chart!.name}\n\nHelm server dry-run passed.',
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
              if (confirmed != true) return;
              await widget.apiClient.applyKubernetesHelmChart(
                widget.clusterId,
                spec,
              );
              if (dialogContext.mounted) Navigator.pop(dialogContext);
              _refresh();
            } catch (error) {
              _showError(error);
            } finally {
              if (dialogContext.mounted) setDialogState(() => busy = false);
            }
          }

          return AlertDialog(
            title: const Text('Install or upgrade Helm chart'),
            content: SizedBox(
              width: 520,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  TextField(
                    controller: name,
                    decoration: const InputDecoration(
                      labelText: 'Release name',
                    ),
                  ),
                  const SizedBox(height: 12),
                  OutlinedButton.icon(
                    onPressed: busy ? null : pickChart,
                    icon: const Icon(Icons.upload_file),
                    label: Text(chart?.name ?? 'Select chart archive (.tgz)'),
                  ),
                  TextField(
                    controller: values,
                    maxLines: 6,
                    decoration: const InputDecoration(
                      labelText: 'Values (JSON)',
                      border: OutlineInputBorder(),
                    ),
                  ),
                ],
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

  Future<void> _history(Map<String, dynamic> release) async {
    final name = '${release['name']}';
    try {
      final versions = await widget.apiClient.kubernetesHelmHistory(
        widget.clusterId,
        _namespace,
        name,
      );
      if (!mounted) return;
      final revision = await showDialog<int>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text('$name history'),
          content: SizedBox(
            width: 440,
            child: ListView(
              shrinkWrap: true,
              children: [
                for (final version in versions)
                  ListTile(
                    title: Text(
                      'Revision ${version['revision']} · ${version['chartVersion']}',
                    ),
                    subtitle: Text('${version['status']}'),
                    onTap: version['revision'] == release['revision']
                        ? null
                        : () => Navigator.pop(
                            context,
                            version['revision'] as int,
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
      if (!mounted) return;
      final approved = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Roll back Helm release?'),
          content: Text('Restore $name to revision $revision in $_namespace?'),
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
      if (approved != true) return;
      await widget.apiClient.rollbackKubernetesHelm(
        widget.clusterId,
        _namespace,
        name,
        revision,
      );
      _refresh();
    } catch (error) {
      _showError(error);
    }
  }

  Future<void> _uninstall(Map<String, dynamic> release) async {
    final name = '${release['name']}';
    final approved = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Uninstall Helm release?'),
        content: Text(
          'Uninstall $name from $_namespace? Chart resources may be removed.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Uninstall'),
          ),
        ],
      ),
    );
    if (approved != true) return;
    try {
      await widget.apiClient.uninstallKubernetesHelm(
        widget.clusterId,
        _namespace,
        name,
      );
      _refresh();
    } catch (error) {
      _showError(error);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text('${widget.clusterName} · Helm'),
      actions: [
        IconButton(
          onPressed: _refresh,
          icon: const Icon(Icons.refresh),
          tooltip: 'Refresh',
        ),
        if (widget.isAdmin)
          IconButton(
            onPressed: _apply,
            icon: const Icon(Icons.add),
            tooltip: 'Install or upgrade',
          ),
      ],
    ),
    body: Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: DropdownButton<String>(
            value: _namespace,
            items: [
              for (final ns in widget.namespaces)
                DropdownMenuItem(value: ns, child: Text(ns)),
            ],
            onChanged: (value) {
              if (value != null) {
                setState(() {
                  _namespace = value;
                  _releases = _load();
                });
              }
            },
          ),
        ),
        Expanded(
          child: FutureBuilder<List<Map<String, dynamic>>>(
            future: _releases,
            builder: (context, snapshot) {
              if (!snapshot.hasData) {
                return Center(
                  child: Text(
                    snapshot.hasError
                        ? '${snapshot.error}'
                        : 'Loading releases…',
                  ),
                );
              }
              if (snapshot.data!.isEmpty) {
                return const Center(
                  child: Text('No Helm releases in this namespace.'),
                );
              }
              return ListView(
                children: [
                  for (final release in snapshot.data!)
                    ListTile(
                      title: Text('${release['name']}'),
                      subtitle: Text(
                        '${release['chart']} ${release['chartVersion']} · ${release['status']} · revision ${release['revision']}',
                      ),
                      trailing: widget.isAdmin
                          ? PopupMenuButton<String>(
                              onSelected: (action) {
                                if (action == 'history') _history(release);
                                if (action == 'uninstall') _uninstall(release);
                              },
                              itemBuilder: (_) => const [
                                PopupMenuItem(
                                  value: 'history',
                                  child: Text('History and rollback'),
                                ),
                                PopupMenuItem(
                                  value: 'uninstall',
                                  child: Text('Uninstall'),
                                ),
                              ],
                            )
                          : null,
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
