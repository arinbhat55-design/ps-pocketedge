import 'dart:convert';

import 'package:app/features/containers/terminal_output_buffer.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('live zsh startup, resize and command redraws remain left aligned', () {
    final output = TerminalOutputBuffer();
    const prompt = '(base) user@host ~ % ';
    final captured =
        '%${' ' * 78}\r \r\r$prompt\x1b[?2004h'
        '\r\r$prompt\x1b[?2004l\r\r\n'
        '%${' ' * 98}\r \r\r$prompt\x1b[?2004h'
        "printf 'host-terminal-ok\\n'\x1b[?2004l\r\r\n"
        'host-terminal-ok\r\n'
        '%${' ' * 98}\r \r\r$prompt\x1b[?2004h';
    for (final byte in utf8.encode(captured)) {
      output.addBytes([byte]);
    }
    output.finish();
    expect(output.toString().split('\n'), [
      prompt.trimRight(),
      "${prompt}printf 'host-terminal-ok\\n'",
      'host-terminal-ok',
      prompt.trimRight(),
    ]);
  });

  test(
    'zsh padded partial-line mark is replaced by the left-aligned prompt',
    () {
      final output = TerminalOutputBuffer();
      output.write('\x1b[1m%\x1b[0m${' ' * 79}\r \r');
      output.write('(base) hariom@arindams-Mac-mini ~ % ');
      expect(output.toString(), '(base) hariom@arindams-Mac-mini ~ %');
      output.write('\r(base) hariom@arindams-Mac-mini ~ % ');
      expect(output.toString(), '(base) hariom@arindams-Mac-mini ~ %');
    },
  );

  test('CR overwrites progress while newlines preserve command output', () {
    final output = TerminalOutputBuffer();
    output.write('progress 10%\rprogress 90%\r\nhello\r\nprompt');
    expect(output.toString(), 'progress 90%\nhello\nprompt');
  });

  test('split escapes, erase line and backspace redraw correctly', () {
    final output = TerminalOutputBuffer();
    output.write('long prompt\rshort\x1b[');
    output.write('K\r\nabc\b \bD\x1b]0;title\x1b');
    output.write('\\');
    expect(output.toString(), 'short\nabD');
  });

  test('UTF-8 characters survive websocket frame boundaries', () {
    final output = TerminalOutputBuffer();
    final bytes = utf8.encode('hé🙂');
    for (final byte in bytes) {
      output.addBytes([byte]);
    }
    output.finish();
    expect(output.toString(), 'hé🙂');
  });
}
