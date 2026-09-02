import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../api/api_client.dart';

/// ANSI CSI escape sequences (cursor movement, color codes, etc.) stripped
/// from pty output before display — this widget is a plain scrollback
/// buffer, not a full VT100 emulator, so control sequences would otherwise
/// show up as visible garbage rather than being interpreted.
final _ansiCsi = RegExp(r'\x1B\[[0-9;?]*[a-zA-Z]');
final _ansiOsc = RegExp(r'\x1B\][^\x07]*\x07');

/// Interactive `docker exec` terminal for one container. Deliberately a
/// simple scrollback buffer (strips ANSI control sequences rather than
/// interpreting them) instead of a full terminal emulator — enough to run
/// commands and read their output without adding a new dependency.
class ContainerTerminalScreen extends StatefulWidget {
  final ApiClient apiClient;
  final String serverId;
  final String containerId;
  final String containerName;

  const ContainerTerminalScreen({
    super.key,
    required this.apiClient,
    required this.serverId,
    required this.containerId,
    required this.containerName,
  });

  @override
  State<ContainerTerminalScreen> createState() =>
      _ContainerTerminalScreenState();
}

class _ContainerTerminalScreenState extends State<ContainerTerminalScreen> {
  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;
  final _buffer = StringBuffer();
  final _scrollController = ScrollController();
  final _inputController = TextEditingController();
  final _inputFocus = FocusNode();
  bool _exited = false;
  String? _exitMessage;

  @override
  void initState() {
    super.initState();
    _connect();
  }

  void _connect() {
    final uri = widget.apiClient.containerExecUri(
      widget.serverId,
      widget.containerId,
    );
    final channel = WebSocketChannel.connect(uri);
    _channel = channel;
    _sub = channel.stream.listen(
      (data) {
        if (data is String) {
          // Only the final "done" control message is sent as text; every
          // other frame (pty output) is binary.
          try {
            final decoded = jsonDecode(data) as Map<String, dynamic>;
            if (decoded['done'] == true) {
              setState(() {
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
        final bytes = data is Uint8List
            ? data
            : Uint8List.fromList(List<int>.from(data as List<dynamic>));
        _appendOutput(utf8.decode(bytes, allowMalformed: true));
      },
      onError: (Object e) {
        setState(() {
          _exited = true;
          _exitMessage = 'Disconnected: $e';
        });
      },
      onDone: () {
        if (mounted && !_exited) {
          setState(() {
            _exited = true;
            _exitMessage = 'Connection closed';
          });
        }
      },
    );
  }

  void _appendOutput(String text) {
    var clean = text
        .replaceAll(_ansiOsc, '')
        .replaceAll(_ansiCsi, '')
        .replaceAll('\r\n', '\n')
        .replaceAll('\r', '');
    setState(() {
      for (final rune in clean.runes) {
        if (rune == 0x08 || rune == 0x7f) {
          // Backspace/DEL — remove the last buffered character.
          final s = _buffer.toString();
          if (s.isNotEmpty) {
            _buffer.clear();
            _buffer.write(s.substring(0, s.length - 1));
          }
        } else {
          _buffer.writeCharCode(rune);
        }
      }
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scrollController.hasClients) return;
      _scrollController.jumpTo(_scrollController.position.maxScrollExtent);
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
                  'exec — ${widget.containerName}',
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
                    _buffer.clear();
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
            color: Colors.orange.withValues(alpha: 0.15),
            child: Text(_exitMessage!, style: const TextStyle(fontSize: 12)),
          ),
        Expanded(
          child: Container(
            width: double.infinity,
            color: Colors.black,
            padding: const EdgeInsets.all(8),
            child: SingleChildScrollView(
              controller: _scrollController,
              child: SelectableText(
                _buffer.toString(),
                style: const TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 12,
                  color: Colors.white,
                ),
              ),
            ),
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
