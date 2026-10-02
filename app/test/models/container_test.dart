import 'package:app/models/container.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('HealthCheckEntry.fromJson', () {
    test('parses non-zero unix timestamps', () {
      final entry = HealthCheckEntry.fromJson({
        'startUnix': 1700000000,
        'endUnix': 1700000005,
        'exitCode': 1,
        'output': 'curl: connection refused',
      });

      expect(
        entry.start,
        DateTime.fromMillisecondsSinceEpoch(1700000000 * 1000),
      );
      expect(entry.end, DateTime.fromMillisecondsSinceEpoch(1700000005 * 1000));
      expect(entry.exitCode, 1);
      expect(entry.output, 'curl: connection refused');
    });

    test('treats a zero unix timestamp as unset (null), not epoch', () {
      final entry = HealthCheckEntry.fromJson({
        'startUnix': 0,
        'endUnix': 0,
        'exitCode': 0,
        'output': '',
      });

      expect(entry.start, isNull);
      expect(entry.end, isNull);
    });

    test('defaults missing fields', () {
      final entry = HealthCheckEntry.fromJson({});
      expect(entry.start, isNull);
      expect(entry.end, isNull);
      expect(entry.exitCode, 0);
      expect(entry.output, '');
    });
  });

  group('ContainerDetail.fromJson', () {
    test('parses a fully populated response', () {
      final detail = ContainerDetail.fromJson({
        'containerId': 'abc123',
        'env': ['FOO=bar', 'BAZ=qux'],
        'restartPolicyName': 'on-failure',
        'restartPolicyMaxRetryCount': 3,
        'healthStatus': 'healthy',
        'healthFailingStreak': 0,
        'restartCount': 2,
        'healthLog': [
          {'startUnix': 100, 'endUnix': 101, 'exitCode': 0, 'output': 'ok'},
        ],
        'command': ['/bin/sh', '-c', 'run.sh'],
        'entrypoint': ['/entrypoint.sh'],
        'workingDir': '/app',
        'labels': {'app': 'web'},
        'image': 'nginx:latest',
      });

      expect(detail.containerId, 'abc123');
      expect(detail.env, ['FOO=bar', 'BAZ=qux']);
      expect(detail.restartPolicyName, 'on-failure');
      expect(detail.restartPolicyMaxRetryCount, 3);
      expect(detail.healthStatus, 'healthy');
      expect(detail.restartCount, 2);
      expect(detail.healthLog, hasLength(1));
      expect(detail.healthLog.single.output, 'ok');
      expect(detail.command, ['/bin/sh', '-c', 'run.sh']);
      expect(detail.entrypoint, ['/entrypoint.sh']);
      expect(detail.workingDir, '/app');
      expect(detail.labels, {'app': 'web'});
      expect(detail.image, 'nginx:latest');
    });

    test('defaults every optional field when absent, matching an older '
        'backend response shape (only the required core fields present)', () {
      final detail = ContainerDetail.fromJson({
        'containerId': 'abc123',
        'restartPolicyName': '',
        'restartPolicyMaxRetryCount': 0,
        'healthStatus': '',
        'healthFailingStreak': 0,
        'restartCount': 0,
      });

      expect(detail.env, isEmpty);
      expect(detail.healthLog, isEmpty);
      expect(detail.command, isEmpty);
      expect(detail.entrypoint, isEmpty);
      expect(detail.workingDir, '');
      expect(detail.labels, isEmpty);
      expect(detail.image, '');
      expect(detail.nanoCpus, 0);
      expect(detail.memoryLimitBytes, 0);
      expect(detail.memoryReservationBytes, 0);
      expect(detail.pidsLimit, 0);
    });

    test('parses resource limit fields when present', () {
      final detail = ContainerDetail.fromJson({
        'containerId': 'abc123',
        'restartPolicyName': '',
        'healthStatus': '',
        'nanoCpus': 1500000000,
        'memoryLimitBytes': 134217728,
        'memoryReservationBytes': 67108864,
        'pidsLimit': 100,
      });

      expect(detail.nanoCpus, 1500000000);
      expect(detail.memoryLimitBytes, 134217728);
      expect(detail.memoryReservationBytes, 67108864);
      expect(detail.pidsLimit, 100);
    });
  });

  group('ContainerConfig.toJson', () {
    test('omits resource limit fields when they are all 0 (unset)', () {
      final json = const ContainerConfig(
        image: 'nginx:latest',
        name: 'web',
      ).toJson();

      expect(json.containsKey('nanoCpus'), isFalse);
      expect(json.containsKey('memoryLimitBytes'), isFalse);
      expect(json.containsKey('memoryReservationBytes'), isFalse);
      expect(json.containsKey('pidsLimit'), isFalse);
    });

    test('includes resource limit fields when set', () {
      final json = const ContainerConfig(
        image: 'nginx:latest',
        name: 'web',
        nanoCpus: 1500000000,
        memoryLimitBytes: 134217728,
        memoryReservationBytes: 67108864,
        pidsLimit: 100,
      ).toJson();

      expect(json['nanoCpus'], 1500000000);
      expect(json['memoryLimitBytes'], 134217728);
      expect(json['memoryReservationBytes'], 67108864);
      expect(json['pidsLimit'], 100);
    });
  });
}
