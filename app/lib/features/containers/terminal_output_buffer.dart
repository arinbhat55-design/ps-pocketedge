import 'dart:convert';

/// Plain terminal scrollback with incremental UTF-8 and line redraw support.
/// Shells use CR to overwrite prompts, including zsh's padded partial-line mark.
class TerminalOutputBuffer {
  final _lines = <List<int>>[[]];
  int _column = 0;
  String _state = '';
  String _parameters = '';
  late final ByteConversionSink _decoder = const Utf8Decoder(
    allowMalformed: true,
  ).startChunkedConversion(_TerminalTextSink(write));

  void addBytes(List<int> bytes) => _decoder.add(bytes);
  void finish() => _decoder.close();

  void write(String text) {
    for (final rune in text.runes) {
      if (_state == 'osc' || _state == 'oscEscape') {
        if (rune == 7 || (_state == 'oscEscape' && rune == 92)) {
          _state = '';
        } else {
          _state = rune == 27 ? 'oscEscape' : 'osc';
        }
        continue;
      }
      if (_state == 'escape') {
        _state = switch (rune) {
          91 => 'csi',
          93 => 'osc',
          _ => '',
        };
        _parameters = '';
        continue;
      }
      if (_state == 'csi') {
        if (rune >= 0x40 && rune <= 0x7e) {
          _control(rune);
          _state = '';
        } else if (_parameters.length < 128) {
          _parameters += String.fromCharCode(rune);
        }
        continue;
      }
      switch (rune) {
        case 27:
          _state = 'escape';
        case 13:
          _column = 0;
        case 10:
          _lines.add([]);
          if (_lines.length > 10000) _lines.removeAt(0);
          _column = 0;
        case 8:
        case 127:
          if (_column > 0) _column--;
        case 9:
          final end = ((_column ~/ 8) + 1) * 8;
          while (_column < end) {
            _put(32);
          }
        default:
          if (rune >= 32) _put(rune);
      }
    }
  }

  void _put(int rune) {
    final line = _lines.last;
    while (line.length <= _column) {
      line.add(32);
    }
    line[_column++] = rune;
  }

  void _control(int command) {
    final value = int.tryParse(_parameters.split(';').first) ?? 0;
    final count = (value > 0 ? value : 1).clamp(1, 65535);
    switch (command) {
      case 67: // CSI C: cursor right
        _column = (_column + count).clamp(0, 65535);
      case 68: // CSI D: cursor left
        _column = (_column - count).clamp(0, 65535);
      case 71: // CSI G: absolute column
        _column = count - 1;
      case 75: // CSI K: erase line
        final line = _lines.last;
        if (value == 2) {
          line.clear();
        } else if (value == 1) {
          for (var i = 0; i <= _column && i < line.length; i++) {
            line[i] = 32;
          }
        } else if (_column < line.length) {
          line.removeRange(_column, line.length);
        }
    }
  }

  @override
  String toString() =>
      _lines.map((line) => String.fromCharCodes(line).trimRight()).join('\n');
}

class _TerminalTextSink implements Sink<String> {
  final void Function(String) write;
  _TerminalTextSink(this.write);

  @override
  void add(String text) => write(text);

  @override
  void close() {}
}
