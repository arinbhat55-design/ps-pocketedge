import 'package:app/models/server_metrics.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('ContainerResourceUsage.fromJson', () {
    test('parses a full sample', () {
      final usage = ContainerResourceUsage.fromJson({
        'containerId': 'c1',
        'recordedAt': '2024-01-02T15:04:05Z',
        'cpuPercent': 42.5,
        'memUsageBytes': 1000000,
        'memLimitBytes': 2000000,
        'memPercent': 50.0,
        'netRxBytes': 100,
        'netTxBytes': 200,
        'blockReadBytes': 300,
        'blockWriteBytes': 400,
        'pids': 12,
      });

      expect(usage.containerId, 'c1');
      expect(usage.recordedAt, DateTime.parse('2024-01-02T15:04:05Z'));
      expect(usage.cpuPercent, 42.5);
      expect(usage.memUsageBytes, 1000000);
      expect(usage.memLimitBytes, 2000000);
      expect(usage.memPercent, 50.0);
      expect(usage.netRxBytes, 100);
      expect(usage.netTxBytes, 200);
      expect(usage.blockReadBytes, 300);
      expect(usage.blockWriteBytes, 400);
      expect(usage.pids, 12);
    });

    test('accepts integer-valued JSON numbers for percent fields', () {
      // Go's encoding/json emits "0" not "0.0" for a zero float64 — make
      // sure the num.toDouble() cast handles that.
      final usage = ContainerResourceUsage.fromJson({
        'containerId': 'c1',
        'recordedAt': '2024-01-02T15:04:05Z',
        'cpuPercent': 0,
        'memUsageBytes': 0,
        'memLimitBytes': 0,
        'memPercent': 0,
        'netRxBytes': 0,
        'netTxBytes': 0,
        'blockReadBytes': 0,
        'blockWriteBytes': 0,
        'pids': 0,
      });

      expect(usage.cpuPercent, 0.0);
      expect(usage.memPercent, 0.0);
    });
  });

  group('ServerUpdate.fromJson', () {
    test('parses containerStats when present', () {
      final update = ServerUpdate.fromJson({
        'resources': {
          'cpuPercent': 10.0,
          'memPercent': 20.0,
          'diskPercent': 30.0,
        },
        'containers': [],
        'containerStats': [
          {
            'containerId': 'c1',
            'recordedAt': '2024-01-02T15:04:05Z',
            'cpuPercent': 5.0,
            'memUsageBytes': 1,
            'memLimitBytes': 2,
            'memPercent': 50.0,
            'netRxBytes': 1,
            'netTxBytes': 1,
            'blockReadBytes': 1,
            'blockWriteBytes': 1,
            'pids': 1,
          },
        ],
        'updatedAt': '2024-01-02T15:04:06Z',
      });

      expect(update.containerStats, hasLength(1));
      expect(update.containerStats.single.containerId, 'c1');
    });

    test(
      'containerStats defaults to empty when the tick did not sample it',
      () {
        final update = ServerUpdate.fromJson({
          'resources': {
            'cpuPercent': 10.0,
            'memPercent': 20.0,
            'diskPercent': 30.0,
          },
          'containers': [],
          'updatedAt': '2024-01-02T15:04:06Z',
        });

        expect(update.containerStats, isEmpty);
      },
    );
  });
}
