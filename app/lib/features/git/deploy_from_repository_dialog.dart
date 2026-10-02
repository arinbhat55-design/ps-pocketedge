import 'package:flutter/material.dart';

import '../../api/api_client.dart';
import '../../models/deployment.dart';
import '../../models/git_repository.dart';
import '../../models/server.dart';

/// Creates a Git-linked Compose file from a Dockerfile, then deploys it.
class DeployFromRepositoryDialog extends StatefulWidget {
  final ApiClient apiClient;
  final GitRepository repository;

  const DeployFromRepositoryDialog({
    super.key,
    required this.apiClient,
    required this.repository,
  });

  @override
  State<DeployFromRepositoryDialog> createState() =>
      _DeployFromRepositoryDialogState();
}

class _DeployFromRepositoryDialogState
    extends State<DeployFromRepositoryDialog> {
  final _name = TextEditingController();
  final _context = TextEditingController(text: '.');
  final _dockerfile = TextEditingController(text: 'Dockerfile');
  final _port = TextEditingController(text: '8080');
  final _env = TextEditingController();
  late final Future<(GitRefs, List<Server>)> _options;
  String? _ref;
  String? _serverId;
  bool _autoDeploy = false;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _name.text = widget.repository.name;
    _options = _loadOptions();
  }

  Future<(GitRefs, List<Server>)> _loadOptions() async {
    final values = await Future.wait<Object>([
      widget.apiClient.listGitRefs(widget.repository.id),
      widget.apiClient.listServers(),
    ]);
    return (values[0] as GitRefs, values[1] as List<Server>);
  }

  @override
  void dispose() {
    _name.dispose();
    _context.dispose();
    _dockerfile.dispose();
    _port.dispose();
    _env.dispose();
    super.dispose();
  }

  Map<String, String> _parseEnv() {
    final result = <String, String>{};
    for (final rawLine in _env.text.split('\n')) {
      final line = rawLine.trim();
      if (line.isEmpty || line.startsWith('#')) continue;
      final separator = line.indexOf('=');
      if (separator < 1) {
        throw const FormatException('Environment entries must use KEY=value.');
      }
      final key = line.substring(0, separator).trim();
      if (!RegExp(r'^[A-Za-z_][A-Za-z0-9_]*$').hasMatch(key)) {
        throw FormatException('Invalid environment variable name: $key');
      }
      result[key] = line.substring(separator + 1);
    }
    return result;
  }

  Future<void> _deploy() async {
    final name = _name.text.trim();
    final contextPath = _context.text.trim();
    final dockerfile = _dockerfile.text.trim();
    final port = int.tryParse(_port.text.trim());
    if (name.isEmpty ||
        _ref == null ||
        _serverId == null ||
        port == null ||
        port < 1 ||
        port > 65535) {
      setState(
        () => _error = 'Name, branch, server, and a valid port are required.',
      );
      return;
    }
    Map<String, String> env;
    try {
      env = _parseEnv();
    } on FormatException catch (e) {
      setState(() => _error = e.message);
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    String? generatedFileName;
    try {
      final dockerfilePath = contextPath == '.' || contextPath.isEmpty
          ? dockerfile
          : '$contextPath/$dockerfile';
      await widget.apiClient.previewGitFile(
        widget.repository.id,
        ref: _ref!,
        path: dockerfilePath,
      );
      final file = await widget.apiClient.generateGitComposeFile(
        widget.repository.id,
        name: name,
        ref: _ref!,
        contextPath: contextPath,
        dockerfile: dockerfile,
        port: port,
        envKeys: env.keys.toList(),
      );
      generatedFileName = file.name;
      final outcome = await widget.apiClient.createComposeDeployment(
        composeFileId: file.id,
        serverId: _serverId!,
        env: env,
        metadata: DeploymentMetadata(gitRef: _ref!, autoDeploy: _autoDeploy),
      );
      if (mounted) Navigator.of(context).pop(outcome.deploymentId);
    } catch (e) {
      if (mounted) {
        setState(
          () => _error = generatedFileName == null
              ? 'Deployment failed: $e'
              : 'Deployment failed: $e. Compose file "$generatedFileName" was created and can be deployed from the Compose tab.',
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('Deploy from ${widget.repository.name}'),
      content: SizedBox(
        width: 520,
        child: FutureBuilder<(GitRefs, List<Server>)>(
          future: _options,
          builder: (context, snapshot) {
            if (!snapshot.hasData) {
              if (snapshot.hasError) {
                return Text(
                  'Could not load branches and servers: ${snapshot.error}',
                );
              }
              return const Center(child: CircularProgressIndicator());
            }
            final (refs, servers) = snapshot.data!;
            final availableRefs = [...refs.branches, ...refs.tags];
            return SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  TextField(
                    controller: _name,
                    decoration: const InputDecoration(
                      labelText: 'Deployment name',
                    ),
                  ),
                  DropdownButtonFormField<String>(
                    initialValue: _ref,
                    decoration: const InputDecoration(
                      labelText: 'Branch or tag',
                    ),
                    items: [
                      for (final ref in availableRefs)
                        DropdownMenuItem(
                          value: ref.name,
                          child: Text(ref.name),
                        ),
                    ],
                    onChanged: _busy
                        ? null
                        : (value) => setState(() => _ref = value),
                  ),
                  TextField(
                    controller: _context,
                    decoration: const InputDecoration(
                      labelText: 'Build context in repository',
                    ),
                  ),
                  TextField(
                    controller: _dockerfile,
                    decoration: const InputDecoration(
                      labelText: 'Dockerfile path within context',
                    ),
                  ),
                  TextField(
                    controller: _port,
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                      labelText: 'Container and host port',
                    ),
                  ),
                  DropdownButtonFormField<String>(
                    initialValue: _serverId,
                    decoration: const InputDecoration(labelText: 'Server'),
                    items: [
                      for (final server in servers)
                        DropdownMenuItem(
                          value: server.id,
                          child: Text('${server.name} (${server.status})'),
                        ),
                    ],
                    onChanged: _busy
                        ? null
                        : (value) => setState(() => _serverId = value),
                  ),
                  TextField(
                    controller: _env,
                    maxLines: 3,
                    decoration: const InputDecoration(
                      labelText: 'Environment variables',
                      hintText: 'KEY=value, one per line',
                    ),
                  ),
                  SwitchListTile(
                    contentPadding: EdgeInsets.zero,
                    title: const Text('Deploy on Git push'),
                    value: _autoDeploy,
                    onChanged: _busy
                        ? null
                        : (value) => setState(() => _autoDeploy = value),
                  ),
                  const Text('The selected server must have builds enabled.'),
                  if (_error != null) ...[
                    const SizedBox(height: 8),
                    Text(
                      _error!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                  ],
                ],
              ),
            );
          },
        ),
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _busy ? null : _deploy,
          child: Text(_busy ? 'Deploying…' : 'Deploy'),
        ),
      ],
    );
  }
}
