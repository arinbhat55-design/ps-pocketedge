import 'package:app/models/deployment_event.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('DeploymentEvent.fromJson', () {
    test('parses triggeredByEmail when present', () {
      final event = DeploymentEvent.fromJson({
        'id': 1,
        'deploymentId': 'd1',
        'phase': 'pending',
        'message': 'deployment created',
        'triggeredByEmail': 'admin@example.com',
        'createdAt': '2026-01-01T00:00:00Z',
      });

      expect(event.triggeredByEmail, 'admin@example.com');
    });

    test('triggeredByEmail is null for an agent-reported event', () {
      final event = DeploymentEvent.fromJson({
        'id': 2,
        'deploymentId': 'd1',
        'phase': 'running',
        'message': '',
        'createdAt': '2026-01-01T00:00:00Z',
      });

      expect(event.triggeredByEmail, isNull);
    });

    test('isTerminal is true for running and failed', () {
      expect(
        DeploymentEvent(
          id: 1,
          deploymentId: 'd1',
          phase: 'running',
          message: '',
          createdAt: DateTime.now(),
        ).isTerminal,
        isTrue,
      );
      expect(
        DeploymentEvent(
          id: 1,
          deploymentId: 'd1',
          phase: 'pending',
          message: '',
          createdAt: DateTime.now(),
        ).isTerminal,
        isFalse,
      );
    });
  });
}
