import 'package:app/models/deployment_preview.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('DeploymentPreviewService.fromJson', () {
    test('parses resource limit fields', () {
      final svc = DeploymentPreviewService.fromJson({
        'name': 'web',
        'image': 'nginx',
        'nanoCpus': 1500000000,
        'memoryLimitBytes': 536870912,
      });

      expect(svc.nanoCpus, 1500000000);
      expect(svc.memoryLimitBytes, 536870912);
    });

    test('defaults resource limits to 0 when absent', () {
      final svc = DeploymentPreviewService.fromJson({
        'name': 'web',
        'image': 'nginx',
      });
      expect(svc.nanoCpus, 0);
      expect(svc.memoryLimitBytes, 0);
    });
  });

  group('DeploymentResourceCheck.fromJson', () {
    test('parses a fully populated check', () {
      final check = DeploymentResourceCheck.fromJson({
        'requestedNanoCpus': 1000000000,
        'requestedMemoryBytes': 268435456,
        'serverTotalCpus': 4,
        'serverTotalMemoryBytes': 8589934592,
        'serverAvailableMemoryBytes': 4294967296,
        'serverAvailableCpuCores': 2.5,
        'serverDiskPercentUsed': 42.0,
        'sufficientMemory': true,
        'sufficientCpu': true,
        'riskScore': 'low',
      });

      expect(check.serverTotalCpus, 4);
      expect(check.serverAvailableCpuCores, 2.5);
      expect(check.riskScore, 'low');
      expect(check.sufficientMemory, isTrue);
    });

    test('parses a high-risk insufficient-resources check', () {
      final check = DeploymentResourceCheck.fromJson({
        'requestedMemoryBytes': 8589934592,
        'serverAvailableMemoryBytes': 1073741824,
        'sufficientMemory': false,
        'riskScore': 'high',
      });

      expect(check.sufficientMemory, isFalse);
      expect(check.riskScore, 'high');
    });
  });

  group('DeploymentPreview.fromJson', () {
    test('resourceCheck is null when absent', () {
      final preview = DeploymentPreview.fromJson({
        'name': 'blog-platform',
        'services': [],
      });
      expect(preview.resourceCheck, isNull);
    });

    test('parses a nested resourceCheck', () {
      final preview = DeploymentPreview.fromJson({
        'name': 'blog-platform',
        'services': [],
        'resourceCheck': {'riskScore': 'medium'},
      });
      expect(preview.resourceCheck, isNotNull);
      expect(preview.resourceCheck!.riskScore, 'medium');
    });

    test('portConflicts defaults to empty when absent', () {
      final preview = DeploymentPreview.fromJson({
        'name': 'blog-platform',
        'services': [],
      });
      expect(preview.portConflicts, isEmpty);
    });

    test('parses portConflicts', () {
      final preview = DeploymentPreview.fromJson({
        'name': 'blog-platform',
        'services': [],
        'portConflicts': [
          {
            'service': 'web',
            'hostPort': 8080,
            'protocol': 'tcp',
            'containerId': 'abc123',
          },
        ],
      });
      expect(preview.portConflicts, hasLength(1));
      expect(preview.portConflicts.first.service, 'web');
      expect(preview.portConflicts.first.hostPort, 8080);
      expect(preview.portConflicts.first.containerId, 'abc123');
    });

    test('parses imageChecks, volumeWarnings, missingSecrets, and networkWarnings', () {
      final preview = DeploymentPreview.fromJson({
        'name': 'blog-platform',
        'services': [],
        'imageChecks': [
          {
            'service': 'web',
            'image': 'nginx',
            'available': true,
            'platforms': ['amd64'],
            'archCompatible': false,
          },
        ],
        'volumeWarnings': [
          {'service': 'db', 'target': 'data', 'message': 'mount path must be absolute'},
        ],
        'missingSecrets': ['DB_PASSWORD'],
        'networkWarnings': ['network "frontend" is declared but not supported'],
      });

      expect(preview.imageChecks, hasLength(1));
      expect(preview.imageChecks.first.available, isTrue);
      expect(preview.imageChecks.first.archCompatible, isFalse);
      expect(preview.imageChecks.first.platforms, ['amd64']);

      expect(preview.volumeWarnings, hasLength(1));
      expect(preview.volumeWarnings.first.message, 'mount path must be absolute');

      expect(preview.missingSecrets, ['DB_PASSWORD']);
      expect(preview.networkWarnings, hasLength(1));
    });
  });

  group('DeploymentImageCheck.fromJson', () {
    test('defaults are safe when unavailable with an error', () {
      final check = DeploymentImageCheck.fromJson({
        'service': 'web',
        'image': 'ghost/does-not-exist',
        'available': false,
        'error': 'MANIFEST_UNKNOWN',
      });
      expect(check.available, isFalse);
      expect(check.error, 'MANIFEST_UNKNOWN');
      expect(check.platforms, isEmpty);
    });
  });
}
