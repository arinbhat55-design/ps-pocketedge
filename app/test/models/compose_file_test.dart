import 'package:app/models/compose_file.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('ComposeFile.fromJson', () {
    test('parses a fully populated response', () {
      final file = ComposeFile.fromJson({
        'id': 'cf1',
        'name': 'blog-platform',
        'content': 'services:\n  web:\n    image: nginx\n',
        'serviceNames': ['web', 'db'],
        'createdAt': '2026-01-01T00:00:00Z',
        'updatedAt': '2026-01-02T00:00:00Z',
      });

      expect(file.id, 'cf1');
      expect(file.name, 'blog-platform');
      expect(file.serviceNames, ['web', 'db']);
      expect(file.createdAt, DateTime.parse('2026-01-01T00:00:00Z'));
    });

    test('defaults missing serviceNames to empty', () {
      final file = ComposeFile.fromJson({
        'id': 'cf1',
        'name': 'x',
        'content': '',
        'createdAt': '2026-01-01T00:00:00Z',
        'updatedAt': '2026-01-01T00:00:00Z',
      });
      expect(file.serviceNames, isEmpty);
    });

    test('defaults missing version to 1', () {
      final file = ComposeFile.fromJson({
        'id': 'cf1',
        'name': 'x',
        'content': '',
        'createdAt': '2026-01-01T00:00:00Z',
        'updatedAt': '2026-01-01T00:00:00Z',
      });
      expect(file.version, 1);
    });

    test('parses an explicit version', () {
      final file = ComposeFile.fromJson({
        'id': 'cf1',
        'name': 'x',
        'content': '',
        'version': 3,
        'createdAt': '2026-01-01T00:00:00Z',
        'updatedAt': '2026-01-01T00:00:00Z',
      });
      expect(file.version, 3);
    });
  });

  group('ComposeFileVersionSummary.fromJson', () {
    test('parses a version history entry', () {
      final version = ComposeFileVersionSummary.fromJson({
        'id': 'v1',
        'versionNumber': 2,
        'name': 'blog-platform',
        'createdAt': '2026-01-01T00:00:00Z',
      });

      expect(version.id, 'v1');
      expect(version.versionNumber, 2);
      expect(version.name, 'blog-platform');
    });
  });

  group('ComposeFileVersion.fromJson', () {
    test('parses a full version snapshot including content', () {
      final version = ComposeFileVersion.fromJson({
        'id': 'v1',
        'composeFileId': 'cf1',
        'versionNumber': 2,
        'name': 'blog-platform',
        'content': 'services:\n  web:\n    image: nginx\n',
        'createdAt': '2026-01-01T00:00:00Z',
      });

      expect(version.composeFileId, 'cf1');
      expect(version.content, contains('image: nginx'));
    });
  });

  group('ComposeServiceDraft', () {
    test('round-trips through toJson/fromJson', () {
      const draft = ComposeServiceDraft(
        name: 'web',
        image: 'nginx:latest',
        command: 'nginx -g "daemon off;"',
        ports: ['8080:80'],
        environment: {'FOO': 'bar'},
        volumes: ['data:/var/www'],
        restart: 'unless-stopped',
      );

      final roundTripped = ComposeServiceDraft.fromJson(draft.toJson());

      expect(roundTripped.name, draft.name);
      expect(roundTripped.image, draft.image);
      expect(roundTripped.command, draft.command);
      expect(roundTripped.ports, draft.ports);
      expect(roundTripped.environment, draft.environment);
      expect(roundTripped.volumes, draft.volumes);
      expect(roundTripped.restart, draft.restart);
    });

    test('toJson omits empty optional fields', () {
      const draft = ComposeServiceDraft(name: 'web', image: 'nginx');
      final json = draft.toJson();
      expect(json.containsKey('command'), isFalse);
      expect(json.containsKey('ports'), isFalse);
      expect(json.containsKey('environment'), isFalse);
      expect(json.containsKey('volumes'), isFalse);
      expect(json.containsKey('restart'), isFalse);
      expect(json.containsKey('nanoCpus'), isFalse);
      expect(json.containsKey('memoryLimitBytes'), isFalse);
      expect(json.containsKey('memoryReservationBytes'), isFalse);
    });

    test('resource limits round-trip through toJson/fromJson', () {
      const draft = ComposeServiceDraft(
        name: 'web',
        image: 'nginx',
        nanoCpus: 1500000000,
        memoryLimitBytes: 536870912,
        memoryReservationBytes: 268435456,
      );

      final roundTripped = ComposeServiceDraft.fromJson(draft.toJson());

      expect(roundTripped.nanoCpus, draft.nanoCpus);
      expect(roundTripped.memoryLimitBytes, draft.memoryLimitBytes);
      expect(
        roundTripped.memoryReservationBytes,
        draft.memoryReservationBytes,
      );
    });

    test('health check fields round-trip through toJson/fromJson', () {
      const draft = ComposeServiceDraft(
        name: 'web',
        image: 'nginx',
        healthCheckTest: 'curl -f http://localhost',
        healthCheckIntervalSeconds: 30,
        healthCheckTimeoutSeconds: 5,
        healthCheckRetries: 3,
        healthCheckStartPeriodSeconds: 10,
      );

      final roundTripped = ComposeServiceDraft.fromJson(draft.toJson());

      expect(roundTripped.healthCheckTest, draft.healthCheckTest);
      expect(
        roundTripped.healthCheckIntervalSeconds,
        draft.healthCheckIntervalSeconds,
      );
      expect(
        roundTripped.healthCheckTimeoutSeconds,
        draft.healthCheckTimeoutSeconds,
      );
      expect(roundTripped.healthCheckRetries, draft.healthCheckRetries);
      expect(
        roundTripped.healthCheckStartPeriodSeconds,
        draft.healthCheckStartPeriodSeconds,
      );
    });

    test('toJson omits health check timing fields when test is empty', () {
      const draft = ComposeServiceDraft(
        name: 'web',
        image: 'nginx',
        healthCheckIntervalSeconds: 30,
      );
      final json = draft.toJson();
      expect(json.containsKey('healthCheckTest'), isFalse);
      expect(json.containsKey('healthCheckIntervalSeconds'), isFalse);
    });
  });

  group('ComposeParseResult.fromJson', () {
    test('parses a valid, visual-editable result', () {
      final result = ComposeParseResult.fromJson({
        'valid': true,
        'serviceNames': ['web'],
        'visualEditable': true,
        'services': [
          {'name': 'web', 'image': 'nginx'},
        ],
      });

      expect(result.valid, isTrue);
      expect(result.visualEditable, isTrue);
      expect(result.services, hasLength(1));
      expect(result.services.first.image, 'nginx');
    });

    test('parses an invalid result with errors', () {
      final result = ComposeParseResult.fromJson({
        'valid': false,
        'errors': ['compose file is empty'],
      });

      expect(result.valid, isFalse);
      expect(result.errors, ['compose file is empty']);
      expect(result.visualEditable, isFalse);
    });
  });
}
