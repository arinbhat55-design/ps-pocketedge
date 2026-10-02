import 'package:app/models/network.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('NetworkSummary.fromJson', () {
    test('parses a fully populated response', () {
      final network = NetworkSummary.fromJson({
        'serverId': 'srv1',
        'serverName': 'prod-1',
        'id': 'net123',
        'name': 'app-net',
        'driver': 'bridge',
        'scope': 'local',
        'internal': true,
        'labels': {'env': 'prod'},
        'containerIds': ['c1', 'c2'],
      });

      expect(network.serverId, 'srv1');
      expect(network.serverName, 'prod-1');
      expect(network.id, 'net123');
      expect(network.name, 'app-net');
      expect(network.driver, 'bridge');
      expect(network.scope, 'local');
      expect(network.internal, isTrue);
      expect(network.labels, {'env': 'prod'});
      expect(network.containerIds, ['c1', 'c2']);
    });

    test('defaults missing fields', () {
      final network = NetworkSummary.fromJson({});
      expect(network.serverId, '');
      expect(network.internal, isFalse);
      expect(network.labels, isEmpty);
      expect(network.containerIds, isEmpty);
    });
  });

  group('NetworkOpResult.fromJson', () {
    test('parses success', () {
      final result = NetworkOpResult.fromJson({
        'success': true,
        'networkId': 'net123',
      });
      expect(result.success, isTrue);
      expect(result.networkId, 'net123');
      expect(result.error, isNull);
    });

    test('parses failure with error message', () {
      final result = NetworkOpResult.fromJson({
        'success': false,
        'error': 'network in use',
      });
      expect(result.success, isFalse);
      expect(result.error, 'network in use');
    });
  });
}
