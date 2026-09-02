import 'package:app/models/volume.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('VolumeSummary.fromJson', () {
    test('parses a fully populated response', () {
      final volume = VolumeSummary.fromJson({
        'serverId': 'srv1',
        'serverName': 'prod-1',
        'name': 'data',
        'driver': 'local',
        'mountpoint': '/var/lib/docker/volumes/data/_data',
        'labels': {'app': 'web'},
        'sizeBytes': 4096,
        'inUseBy': ['c1'],
        'orphaned': false,
        'createdUnix': 1700000000,
      });

      expect(volume.serverId, 'srv1');
      expect(volume.name, 'data');
      expect(volume.driver, 'local');
      expect(volume.sizeBytes, 4096);
      expect(volume.inUseBy, ['c1']);
      expect(volume.orphaned, isFalse);
      expect(
        volume.createdAt,
        DateTime.fromMillisecondsSinceEpoch(1700000000 * 1000),
      );
    });

    test('treats a zero createdUnix as unset (null), not epoch', () {
      final volume = VolumeSummary.fromJson({
        'name': 'data',
        'createdUnix': 0,
      });
      expect(volume.createdAt, isNull);
    });

    test('empty inUseBy defaults orphaned to true when absent', () {
      final volume = VolumeSummary.fromJson({'name': 'orphan'});
      expect(volume.inUseBy, isEmpty);
      expect(volume.orphaned, isTrue);
    });
  });

  group('VolumeOpResult.fromJson', () {
    test('parses an in-use failure', () {
      final result = VolumeOpResult.fromJson({
        'success': false,
        'error': 'volume "data" is in use by container(s) [c1]',
        'name': 'data',
      });
      expect(result.success, isFalse);
      expect(result.error, contains('in use'));
      expect(result.name, 'data');
    });
  });

  group('PortConflictResult.fromJson', () {
    test('parses a conflict', () {
      final result = PortConflictResult.fromJson({
        'conflict': true,
        'containerId': 'c1',
      });
      expect(result.conflict, isTrue);
      expect(result.containerId, 'c1');
    });

    test('defaults to no conflict', () {
      final result = PortConflictResult.fromJson({});
      expect(result.conflict, isFalse);
      expect(result.containerId, isNull);
    });
  });
}
