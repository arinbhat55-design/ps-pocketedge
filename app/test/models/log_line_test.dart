import 'package:app/models/log_line.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('LogLine.fromJson', () {
    test('parses a line with a timestamp', () {
      final line = LogLine.fromJson({
        'containerId': 'c1',
        'timestampUnixNano': 1700000000000000000,
        'stream': 'stderr',
        'message': 'panic: nil pointer',
      });

      expect(line.containerId, 'c1');
      expect(line.stream, 'stderr');
      expect(line.message, 'panic: nil pointer');
      expect(line.timestamp, isNotNull);
      expect(line.timestamp!.millisecondsSinceEpoch, 1700000000000);
    });

    test('a zero timestamp is treated as unset, not epoch', () {
      final line = LogLine.fromJson({
        'containerId': 'c1',
        'timestampUnixNano': 0,
        'stream': 'stdout',
        'message': 'hi',
      });
      expect(line.timestamp, isNull);
    });

    test(
      'defaults stream to stdout and containerId/message to empty when absent',
      () {
        final line = LogLine.fromJson({});
        expect(line.containerId, '');
        expect(line.stream, 'stdout');
        expect(line.message, '');
        expect(line.timestamp, isNull);
      },
    );
  });

  group('LogChunk.fromJson', () {
    test('parses lines, done, and error', () {
      final chunk = LogChunk.fromJson({
        'containerId': 'c1',
        'lines': [
          {
            'containerId': 'c1',
            'timestampUnixNano': 0,
            'stream': 'stdout',
            'message': 'line one',
          },
        ],
        'done': true,
        'error': 'container removed mid-stream',
      });

      expect(chunk.containerId, 'c1');
      expect(chunk.lines, hasLength(1));
      expect(chunk.lines.single.message, 'line one');
      expect(chunk.done, isTrue);
      expect(chunk.error, 'container removed mid-stream');
    });

    test('done defaults to false and error to null when absent', () {
      final chunk = LogChunk.fromJson({'containerId': 'c1', 'lines': []});
      expect(chunk.done, isFalse);
      expect(chunk.error, isNull);
      expect(chunk.lines, isEmpty);
    });
  });

  group('ContainerEvent.fromJson', () {
    test('parses a health_status event', () {
      final event = ContainerEvent.fromJson({
        'timestampUnix': 1700000000,
        'type': 'container',
        'action': 'health_status: unhealthy',
        'containerId': 'c1',
        'containerName': 'web',
      });

      expect(
        event.timestamp,
        DateTime.fromMillisecondsSinceEpoch(1700000000 * 1000),
      );
      expect(event.type, 'container');
      expect(event.action, 'health_status: unhealthy');
      expect(event.containerId, 'c1');
      expect(event.containerName, 'web');
    });
  });

  group('LogAnalysis.fromJson', () {
    test('parses a full AI-analysis response', () {
      final analysis = LogAnalysis.fromJson({
        'summary': 'The service crashed twice due to OOM.',
        'rootCause': 'Memory limit too low for the workload.',
        'recommendation': 'Raise the memory limit to 512MB.',
      });

      expect(analysis.summary, 'The service crashed twice due to OOM.');
      expect(analysis.rootCause, 'Memory limit too low for the workload.');
      expect(analysis.recommendation, 'Raise the memory limit to 512MB.');
    });

    test('defaults every field to empty string when absent', () {
      final analysis = LogAnalysis.fromJson({});
      expect(analysis.summary, '');
      expect(analysis.rootCause, '');
      expect(analysis.recommendation, '');
    });
  });
}
