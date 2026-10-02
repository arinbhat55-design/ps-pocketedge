import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';
import '../../theme/app_theme.dart';
import 'terminal_output_buffer.dart';

/// Use an installed fixed-width font and measure this same style for PTY sizing.
const _terminalStyle = TextStyle(
  fontFamily: 'Menlo',
  fontFamilyFallback: ['Consolas', 'DejaVu Sans Mono', 'monospace'],
  fontSize: 12,
  color: Colors.white,
);

/// Interactive host/container shell with scrollback and line redraw support.
/// Full-screen terminal applications are not supported.
class ContainerTerminalScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String containerName;
  final bool hostShell;

  const ContainerTerminalScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.containerName,
  }) : hostShell = false;

  const ContainerTerminalScreen.host({
    super.key,
    required this.apiClient,
    required this.serverId,
    required String serverName,
  }) : containerId = '',
       containerName = serverName,
       hostShell = true;

  @override
  State<ContainerTerminalScreen> createState() =>
      _ContainerTerminalScreenState();
}

class _ContainerTerminalScreenState extends State<ContainerTerminalScreen> {
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;
  var _buffer = TerminalOutputBuffer();
  final _scrollController = ScrollController();
  final _inputController = TextEditingController();
  final _inputFocus = FocusNode();
  bool _exited = false;
  int? _terminalCols;
  int? _terminalRows;
  String? _exitMessage;

  @override
  void initState() {
    super.initState();
    _connect();
  }

  void _connect() {
    _terminalCols = null;
    _terminalRows = null;
    _sub?.cancel();
    _channel?.sink.close();
    final uri = widget.hostShell
        ? widget.apiClient.hostExecUri(widget.serverId)
        : widget.apiClient.containerExecUri(
            widget.serverId,
            widget.containerId,
          );
    final channel = WebSocketChannel.connect(uri);
    _channel = channel;
    _sub = channel.stream.listen(
      (data) {
        if (!mounted || _channel != channel) return;
        if (data is String) {
          // Only the final "done" control message is sent as text; every
          // other frame (pty output) is binary.
          try {
            final decoded = jsonDecode(data) as Map<String, dynamic>;
            if (decoded['done'] == true) {
              setState(() {
                _buffer.finish();
                _exited = true;
                final err = decoded['error'] as String?;
                _exitMessage = (err != null && err.isNotEmpty)
                    ? 'Session ended: $err'
                    : 'Session ended (exit code ${decoded['exitCode']})';
              });
              return;
            }
          } catch (_) {
            // Not JSON — fall through and display as text output.
          }
        }
        if (data is String) {
          setState(() => _buffer.write(data));
          _scrollToBottom();
          return;
        }
        final bytes = data is Uint8List
            ? data
            : Uint8List.fromList(List<int>.from(data as List<dynamic>));
        setState(() => _buffer.addBytes(bytes));
        _scrollToBottom();
      },
      onError: (Object e) {
        if (!mounted || _channel != channel) return;
        setState(() {
          _exited = true;
          _exitMessage = 'Disconnected: $e';
        });
      },
      onDone: () {
        if (mounted && _channel == channel && !_exited) {
          setState(() {
            _buffer.finish();
            _exited = true;
            _exitMessage = 'Connection closed';
          });
        }
      },
    );
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scrollController.hasClients) return;
      _scrollController.jumpTo(_scrollController.position.maxScrollExtent);
    });
  }

  void _resizeTerminal(BoxConstraints bounds) {
    if (!bounds.maxWidth.isFinite || !bounds.maxHeight.isFinite) return;
    final character = TextPainter(
      text: const TextSpan(text: 'M', style: _terminalStyle),
      textDirection: TextDirection.ltr,
      textScaler: MediaQuery.textScalerOf(context),
    )..layout();
    final cols = ((bounds.maxWidth - 16) / character.width).floor().clamp(
      10,
      65535,
    );
    final rows = ((bounds.maxHeight - 16) / character.height).floor().clamp(
      2,
      65535,
    );
    character.dispose();
    if (cols == _terminalCols && rows == _terminalRows) return;
    _terminalCols = cols;
    _terminalRows = rows;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && !_exited) _channel?.sink.add('resize:${cols}x$rows');
    });
  }

  void _sendText(String text) {
    _channel?.sink.add(Uint8List.fromList(utf8.encode(text)));
  }

  void _sendLine() {
    final text = _inputController.text;
    _inputController.clear();
    _sendText('$text\n');
    _inputFocus.requestFocus();
  }

  void _sendCtrlC() => _sendText('\x03');

  @override
  void dispose() {
    _sub?.cancel();
    _channel?.sink.close();
    _scrollController.dispose();
    _inputController.dispose();
    _inputFocus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          color: Theme.of(context).colorScheme.surfaceContainerHighest,
          child: Row(
            children: [
              Expanded(
                child: Text(
                  widget.hostShell
                      ? 'Host shell — ${widget.containerName}'
                      : 'exec — ${widget.containerName}',
                  style: Theme.of(context).textTheme.labelMedium,
                ),
              ),
              if (!_exited)
                IconButton(
                  tooltip: 'Send Ctrl+C',
                  icon: const Text(
                    '^C',
                    style: TextStyle(fontWeight: FontWeight.bold),
                  ),
                  onPressed: _sendCtrlC,
                ),
              if (_exited)
                TextButton.icon(
                  onPressed: () => setState(() {
                    _exited = false;
                    _exitMessage = null;
                    _buffer = TerminalOutputBuffer();
                    _connect();
                  }),
                  icon: const Icon(Icons.refresh, size: 16),
                  label: const Text('Reconnect'),
                ),
            ],
          ),
        ),
        if (_exitMessage != null)
          Container(
            width: double.infinity,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            color: AppColors.warning.withValues(alpha: 0.15),
            child: Text(_exitMessage!, style: const TextStyle(fontSize: 12)),
          ),
        Expanded(
          child: LayoutBuilder(
            builder: (context, bounds) {
              _resizeTerminal(bounds);
              return Container(
                width: double.infinity,
                color: Colors.black,
                padding: const EdgeInsets.all(8),
                child: SingleChildScrollView(
                  controller: _scrollController,
                  child: SelectableText(
                    _buffer.toString(),
                    style: _terminalStyle,
                    textAlign: TextAlign.left,
                    textDirection: TextDirection.ltr,
                  ),
                ),
              );
            },
          ),
        ),
        Padding(
          padding: const EdgeInsets.all(8),
          child: Row(
            children: [
              Expanded(
                child: TextField(
                  controller: _inputController,
                  focusNode: _inputFocus,
                  enabled: !_exited,
                  autofocus: true,
                  style: const TextStyle(fontFamily: 'monospace'),
                  decoration: const InputDecoration(
                    isDense: true,
                    border: OutlineInputBorder(),
                    hintText: 'Type a command and press Enter',
                  ),
                  onSubmitted: (_) => _exited ? null : _sendLine(),
                ),
              ),
              const SizedBox(width: 8),
              IconButton(
                icon: const Icon(Icons.send),
                onPressed: _exited ? null : _sendLine,
              ),
            ],
          ),
        ),
      ],
    );
  }
}
