import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../theme/app_theme.dart';

class AddServerDialog extends StatefulWidget {
  final ApiClient apiClient;

  const AddServerDialog({super.key, required this.apiClient});

  @override
  State<AddServerDialog> createState() => _AddServerDialogState();
}

class _AddServerDialogState extends State<AddServerDialog> {
  EnrollmentToken? _token;
  String? _error;
  String _platform = 'Linux';
  String _runtime = 'docker';

  static const _scripts =
      'https://raw.githubusercontent.com/ankitapaul1586-cmd/pspocketedge/master/scripts';

  String _command(EnrollmentToken token) {
    final script = _platform == 'macOS'
        ? 'install-agent-macos.sh'
        : 'install-agent.sh';
    final sudo = _platform == 'macOS' ? '' : 'sudo ';
    return 'curl -fsSL $_scripts/$script | ${sudo}sh -s -- '
        '--server=<control-plane-host>:8443 --token=${token.token}'
        ' --runtime=$_runtime';
  }

  @override
  void initState() {
    super.initState();
    _generate();
  }

  Future<void> _generate() async {
    setState(() {
      _token = null;
      _error = null;
    });
    try {
      final token = await widget.apiClient.createEnrollmentToken();
      if (mounted) setState(() => _token = token);
    } catch (e) {
      if (mounted) setState(() => _error = 'Failed to generate token: $e');
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Add server'),
      scrollable: true,
      content: SizedBox(
        width: 560,
        child: _error != null
            ? Text(_error!, style: const TextStyle(color: AppColors.failed))
            : _token == null
            ? const Center(
                child: Padding(
                  padding: EdgeInsets.all(24),
                  child: CircularProgressIndicator(),
                ),
              )
            : Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text(
                    'Choose the machine running your containers. The token is single-use.',
                  ),
                  const SizedBox(height: 12),
                  SegmentedButton<String>(
                    segments: const [
                      ButtonSegment(value: 'Linux', label: Text('Linux')),
                      ButtonSegment(value: 'macOS', label: Text('macOS')),
                      ButtonSegment(value: 'Windows', label: Text('Windows')),
                    ],
                    selected: {_platform},
                    onSelectionChanged: (value) =>
                        setState(() => _platform = value.first),
                  ),
                  const SizedBox(height: 12),
                  SegmentedButton<String>(
                    segments: const [
                      ButtonSegment(value: 'docker', label: Text('Docker')),
                      ButtonSegment(value: 'podman', label: Text('Podman')),
                    ],
                    selected: {_runtime},
                    onSelectionChanged: (value) =>
                        setState(() => _runtime = value.first),
                  ),
                  const SizedBox(height: 12),
                  Text(
                    _runtime == 'podman'
                        ? (_platform == 'macOS'
                              ? 'Install Podman, then run podman machine init and podman machine start. Run the command below without sudo. Add --allow-builds to enable image builds.'
                              : 'Install Podman on Linux (inside WSL2 for Windows) with systemd enabled. This command enables the rootful Podman socket. Add --allow-builds to enable image builds.')
                        : switch (_platform) {
                            'macOS' =>
                              'Install Docker CLI and Colima, then run colima start. Run the command below in Terminal without sudo. Builds are disabled until you add --allow-builds.',
                            'Windows' =>
                              'Run this command inside an Ubuntu WSL2 distribution with Docker Engine and systemd enabled. It does not install Docker Engine.',
                            _ =>
                              'Run this on a Linux machine with Docker Engine installed and running. Builds are disabled until you add --allow-builds.',
                          },
                  ),
                  const SizedBox(height: 12),
                  _CopyableCommand(command: _command(_token!)),
                  const SizedBox(height: 8),
                  const Text(
                    'Replace <control-plane-host> with the gRPC host reachable from this machine.',
                  ),
                  const SizedBox(height: 8),
                  Text(
                    'Token expires: ${_token!.expiresAt.toLocal()}',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
              ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
      ],
    );
  }
}

class _CopyableCommand extends StatelessWidget {
  final String command;

  const _CopyableCommand({required this.command});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Expanded(
            child: SelectableText(
              command,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
            ),
          ),
          IconButton(
            icon: const Icon(Icons.copy, size: 18),
            tooltip: 'Copy',
            onPressed: () {
              Clipboard.setData(ClipboardData(text: command));
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(content: Text('Copied install command')),
              );
            },
          ),
        ],
      ),
    );
  }
}
