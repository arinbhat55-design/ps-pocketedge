import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../models/build.dart';
import '../../models/image.dart';
import 'deployment_widgets.dart';

/// Replays the stored output, then follows the agent's live build stream.
class BuildLogsScreen extends StatefulWidget {
  final ApiClient apiClient;
  final ImageBuild initialBuild;
  final bool isAdmin;

  const BuildLogsScreen({
    super.key,
    required this.apiClient,
    required this.initialBuild,
    this.isAdmin = false,
  });

  @override
  State<BuildLogsScreen> createState() => _BuildLogsScreenState();
}

class _BuildLogsScreenState extends State<BuildLogsScreen> {
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _subscription;
  late ImageBuild _build = widget.initialBuild;
  String _log = '';
  int _lastSeq = 0;
  String? _error;
  bool _cancelling = false;
  bool _pushing = false;
  String? _pushedRef;
  final ScrollController _scroll = ScrollController();
  bool _followOutput = true;

  void _scrollToEnd() {
    if (!_followOutput) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && _scroll.hasClients) {
        _scroll.jumpTo(_scroll.position.maxScrollExtent);
      }
    });
  }

  @override
  void initState() {
    super.initState();
    _connect();
  }

  void _connect() {
    final channel = WebSocketChannel.connect(
      widget.apiClient.buildStreamUri(_build.id),
    );
    _channel = channel;
    _subscription = channel.stream.listen(
      (data) {
        final message = jsonDecode(data as String) as Map<String, dynamic>;
        if (!mounted) return;
        setState(() {
          if (message['type'] == 'build') {
            _build = ImageBuild.fromJson(
              message['build'] as Map<String, dynamic>,
            );
            _log = _build.log;
            // The server's snapshots include stored output. Live chunks
            // with older sequence numbers have already been replayed.
            _lastSeq =
                ((message['build'] as Map<String, dynamic>)['logSeq'] as num?)
                    ?.toInt() ??
                0;
          } else if (message['type'] == 'log') {
            final seq = (message['seq'] as num?)?.toInt() ?? 0;
            if (seq > _lastSeq) {
              _lastSeq = seq;
              _log += message['text'] as String? ?? '';
            }
          } else if (message['type'] == 'status') {
            _refreshBuild();
          }
        });
        _scrollToEnd();
      },
      onError: (Object error) {
        if (mounted) {
          setState(() => _error = 'Live output disconnected: $error');
        }
      },
    );
  }

  Future<void> _refreshBuild() async {
    try {
      final build = await widget.apiClient.getBuild(_build.id);
      if (mounted) setState(() => _build = build);
    } catch (_) {}
  }

  Future<void> _cancel() async {
    setState(() => _cancelling = true);
    try {
      await widget.apiClient.cancelBuild(_build.id);
      await _refreshBuild();
    } catch (error) {
      if (mounted) setState(() => _error = 'Could not cancel build: $error');
    } finally {
      if (mounted) setState(() => _cancelling = false);
    }
  }

  Future<void> _push() async {
    setState(() {
      _pushing = true;
      _error = null;
    });
    try {
      final registries = await widget.apiClient.listRegistries();
      if (!mounted) return;
      if (registries.isEmpty) {
        setState(() => _error = 'Add a registry in Images → Registries first.');
        return;
      }
      final choice = await showDialog<({String registryId, String repository})>(
        context: context,
        builder: (_) => _PushBuildDialog(build: _build, registries: registries),
      );
      if (choice == null) return;
      final ref = await widget.apiClient.pushBuild(
        _build.id,
        registryId: choice.registryId,
        repository: choice.repository,
      );
      if (mounted) setState(() => _pushedRef = ref);
    } catch (error) {
      if (mounted) setState(() => _error = 'Could not push image: $error');
    } finally {
      if (mounted) setState(() => _pushing = false);
    }
  }

  @override
  void dispose() {
    _subscription?.cancel();
    _channel?.sink.close();
    _scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text('Build: ${_build.service}')),
    body: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.all(16),
          child: Wrap(
            spacing: 12,
            runSpacing: 8,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              Chip(label: Text(humanizePhase(_build.status))),
              Text('Revision ${_build.revision}'),
              if (_build.gitCommit.isNotEmpty)
                Tooltip(
                  message: _build.gitCommit,
                  child: Text(
                    'Commit ${_build.gitCommit.length > 7 ? _build.gitCommit.substring(0, 7) : _build.gitCommit}',
                  ),
                ),
              if (_build.reused) const Text('Reused existing image'),
              OutlinedButton.icon(
                onPressed: _log.isEmpty
                    ? null
                    : () => Clipboard.setData(ClipboardData(text: _log)),
                icon: const Icon(Icons.copy_all_outlined),
                label: const Text('Copy log'),
              ),
              FilterChip(
                label: const Text('Follow output'),
                selected: _followOutput,
                onSelected: (value) {
                  setState(() => _followOutput = value);
                  if (value) _scrollToEnd();
                },
              ),
              if (widget.isAdmin && _build.status == 'succeeded')
                OutlinedButton.icon(
                  onPressed: _pushing ? null : _push,
                  icon: const Icon(Icons.cloud_upload_outlined),
                  label: Text(_pushing ? 'Pushing…' : 'Push to registry'),
                ),
              if (widget.isAdmin && !_build.isFinished)
                OutlinedButton.icon(
                  onPressed: _cancelling ? null : _cancel,
                  icon: const Icon(Icons.stop),
                  label: const Text('Cancel build'),
                ),
            ],
          ),
        ),
        if (_build.statusMessage.isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text(_build.statusMessage),
          ),
        if (_pushedRef != null)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: SelectableText('Published as $_pushedRef'),
          ),
        if (_error != null)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text(
              _error!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ),
        const Divider(),
        Expanded(
          child: SelectionArea(
            child: SingleChildScrollView(
              controller: _scroll,
              padding: const EdgeInsets.all(16),
              child: Text(
                _log.isEmpty ? 'Waiting for build output…' : _log,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              ),
            ),
          ),
        ),
      ],
    ),
  );
}

class _PushBuildDialog extends StatefulWidget {
  final ImageBuild build;
  final List<Registry> registries;

  const _PushBuildDialog({required this.build, required this.registries});

  @override
  State<_PushBuildDialog> createState() => _PushBuildDialogState();
}

class _PushBuildDialogState extends State<_PushBuildDialog> {
  late String _registryId = widget.registries.first.id;
  late final TextEditingController _repository = TextEditingController(
    text:
        'pspe/${widget.build.deploymentId.replaceAll('-', '').substring(0, 8)}-${widget.build.service.toLowerCase()}',
  );

  @override
  void dispose() {
    _repository.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('Push built image'),
    content: SizedBox(
      width: 440,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          DropdownButtonFormField<String>(
            initialValue: _registryId,
            decoration: const InputDecoration(labelText: 'Registry'),
            items: [
              for (final registry in widget.registries)
                DropdownMenuItem(
                  value: registry.id,
                  child: Text(registry.name),
                ),
            ],
            onChanged: (value) =>
                setState(() => _registryId = value ?? _registryId),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _repository,
            decoration: const InputDecoration(
              labelText: 'Repository path',
              helperText: 'Lowercase path within the selected registry',
            ),
          ),
        ],
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('Cancel'),
      ),
      FilledButton(
        onPressed: () => Navigator.pop(context, (
          registryId: _registryId,
          repository: _repository.text.trim(),
        )),
        child: const Text('Push'),
      ),
    ],
  );
}
