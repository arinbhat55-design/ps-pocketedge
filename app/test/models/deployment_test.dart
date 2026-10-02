import 'package:app/models/deployment.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('Deployment.fromJson', () {
    test('parses governance fields', () {
      final deployment = Deployment.fromJson({
        'id': 'd1',
        'serverId': 's1',
        'deployEnvironment': 'production',
        'tags': ['team-a'],
        'changeRequest': 'JIRA-1234',
        'notes': 'promoted after QA sign-off',
        'createdAt': '2026-01-01T00:00:00Z',
        'updatedAt': '2026-01-02T00:00:00Z',
      });

      expect(deployment.deployEnvironment, 'production');
      expect(deployment.tags, ['team-a']);
      expect(deployment.changeRequest, 'JIRA-1234');
      expect(deployment.notes, 'promoted after QA sign-off');
    });

    test('defaults governance fields when absent', () {
      final deployment = Deployment.fromJson({
        'id': 'd1',
        'serverId': 's1',
        'createdAt': '2026-01-01T00:00:00Z',
        'updatedAt': '2026-01-01T00:00:00Z',
      });

      expect(deployment.deployEnvironment, isNull);
      expect(deployment.tags, isEmpty);
      expect(deployment.changeRequest, '');
      expect(deployment.notes, '');
    });
  });
}
