import 'package:flutter_test/flutter_test.dart';

import 'package:app/models/image.dart';

void main() {
  group('formatBytes', () {
    test('zero and negative sizes render as 0 B', () {
      expect(formatBytes(0), '0 B');
      expect(formatBytes(-5), '0 B');
    });

    test('stays in bytes below 1024', () {
      expect(formatBytes(512), '512 B');
    });

    test('crosses into KB/MB/GB at 1024-multiples', () {
      expect(formatBytes(1024), '1 KB');
      expect(formatBytes(1024 * 1024), '1 MB');
      expect(formatBytes(1024 * 1024 * 1024), '1 GB');
    });

    test('shows one decimal place for small values in a unit', () {
      // 1536 bytes = 1.5 KB — below the "10 or more" threshold that drops
      // to a whole number, so the decimal place is kept.
      expect(formatBytes(1536), '1.5 KB');
    });

    test('drops the decimal place once the value reaches double digits', () {
      // 15 MB, comfortably >= 10, should render as a whole number.
      expect(formatBytes(15 * 1024 * 1024), '15 MB');
    });
  });

  group('ImageSummary', () {
    test('shortId strips the sha256: prefix and truncates to 12 chars', () {
      final img = ImageSummary(
        serverId: 's1',
        serverName: 'server-1',
        id: 'sha256:6baf43584bcb78f2e5847d1de515f23499913ac9f12bdf834811a3145eb11ca1',
        repoTags: const ['alpine:3.19'],
        repoDigests: const [],
        sizeBytes: 100,
        createdUnix: 1000,
        dangling: false,
        containersCount: 0,
      );
      expect(img.shortId, '6baf43584bcb');
    });

    test('shortId does not crash on an id shorter than 19 chars', () {
      // A regression test: shortId used to do a fixed substring(7, 19),
      // which threw a RangeError for any sha256:-prefixed id under 19
      // characters and took the whole image list down with it.
      final img = ImageSummary(
        serverId: 's1',
        serverName: 'server-1',
        id: 'sha256:def456',
        repoTags: const [],
        repoDigests: const [],
        sizeBytes: 100,
        createdUnix: 1000,
        dangling: true,
        containersCount: 0,
      );
      expect(img.shortId, 'def456');
    });

    test('shortId truncates a bare (non-prefixed) id the same way', () {
      final img = ImageSummary(
        serverId: 's1',
        serverName: 'server-1',
        id: '6baf43584bcb78f2e5847d1de515f23499913ac9f12bdf834811a3145eb11ca1',
        repoTags: const [],
        repoDigests: const [],
        sizeBytes: 100,
        createdUnix: 1000,
        dangling: true,
        containersCount: 0,
      );
      expect(img.shortId, '6baf43584bcb');
    });

    test('createdAt converts the unix seconds field to a DateTime', () {
      final img = ImageSummary(
        serverId: 's1',
        serverName: 'server-1',
        id: 'sha256:abc',
        repoTags: const [],
        repoDigests: const [],
        sizeBytes: 100,
        createdUnix: 1759921840,
        dangling: false,
        containersCount: 0,
      );
      expect(img.createdAt, DateTime.fromMillisecondsSinceEpoch(1759921840000));
    });

    test('fromJson round-trips a real backend response shape', () {
      final img = ImageSummary.fromJson(const {
        'serverId': 's1',
        'serverName': 'my-server',
        'id': 'sha256:abc',
        'repoTags': ['nginx:latest'],
        'repoDigests': ['nginx@sha256:abc'],
        'sizeBytes': 12345,
        'createdUnix': 1700000000,
        'dangling': false,
        'containersCount': 2,
      });
      expect(img.serverId, 's1');
      expect(img.repoTags, ['nginx:latest']);
      expect(img.containersCount, 2);
      expect(img.dangling, false);
    });

    test('fromJson defaults missing optional fields safely', () {
      final img = ImageSummary.fromJson(const {
        'serverId': 's1',
        'serverName': 'my-server',
        'id': 'sha256:abc',
      });
      expect(img.repoTags, isEmpty);
      expect(img.repoDigests, isEmpty);
      expect(img.sizeBytes, 0);
      expect(img.dangling, false);
      expect(img.containersCount, 0);
    });
  });

  group('ImageDetail', () {
    test('fromJson parses layers, env, and labels', () {
      final detail = ImageDetail.fromJson(const {
        'id': 'sha256:abc',
        'repoTags': ['alpine:3.19'],
        'repoDigests': ['alpine@sha256:abc'],
        'sizeBytes': 100,
        'createdUnix': 1000,
        'architecture': 'arm64',
        'os': 'linux',
        'layers': [
          {'digest': 'sha256:layer1', 'sizeBytes': 50},
        ],
        'env': ['PATH=/usr/bin'],
        'labels': {'maintainer': 'someone'},
      });
      expect(detail.architecture, 'arm64');
      expect(detail.layers, hasLength(1));
      expect(detail.layers.first.digest, 'sha256:layer1');
      expect(detail.env, ['PATH=/usr/bin']);
      expect(detail.labels['maintainer'], 'someone');
    });

    test('fromJson defaults missing fields to empty collections', () {
      final detail = ImageDetail.fromJson(const {'id': 'sha256:abc'});
      expect(detail.repoTags, isEmpty);
      expect(detail.layers, isEmpty);
      expect(detail.env, isEmpty);
      expect(detail.labels, isEmpty);
    });
  });

  group('ImageUpdateStatus', () {
    test('fromJson parses an up-to-date result', () {
      final status = ImageUpdateStatus.fromJson(const {
        'upToDate': true,
        'localDigest': 'alpine@sha256:abc',
        'remoteDigest': 'sha256:abc',
      });
      expect(status.upToDate, true);
      expect(status.localDigest, 'alpine@sha256:abc');
    });

    test('fromJson defaults upToDate to true when absent', () {
      final status = ImageUpdateStatus.fromJson(const {});
      expect(status.upToDate, true);
    });
  });

  group('ScanResult', () {
    test('totalCount sums every severity bucket', () {
      const result = ScanResult(
        criticalCount: 1,
        highCount: 2,
        mediumCount: 3,
        lowCount: 4,
        unknownCount: 5,
      );
      expect(result.totalCount, 15);
    });

    test('fromJson parses vulnerabilities and counts', () {
      final result = ScanResult.fromJson(const {
        'criticalCount': 1,
        'highCount': 0,
        'mediumCount': 0,
        'lowCount': 0,
        'unknownCount': 0,
        'vulnerabilities': [
          {
            'id': 'CVE-2024-1',
            'pkgName': 'openssl',
            'severity': 'CRITICAL',
            'fixedVersion': '3.0.1',
          },
        ],
      });
      expect(result.criticalCount, 1);
      expect(result.vulnerabilities, hasLength(1));
      expect(result.vulnerabilities.first.pkgName, 'openssl');
    });
  });

  group('Registry', () {
    test('fromJson never expects a password field (never sent by the API)', () {
      final registry = Registry.fromJson(const {
        'id': 'r1',
        'name': 'acme',
        'url': 'registry.acme.com',
        'username': 'bot',
      });
      expect(registry.id, 'r1');
      expect(registry.username, 'bot');
    });
  });

  group('ApprovedImage', () {
    test('fromJson parses pattern and optional note', () {
      final approved = ApprovedImage.fromJson(const {
        'id': 'a1',
        'pattern': 'nginx',
        'note': 'web server',
      });
      expect(approved.pattern, 'nginx');
      expect(approved.note, 'web server');
    });

    test('note defaults to empty string when absent', () {
      final approved = ApprovedImage.fromJson(const {
        'id': 'a1',
        'pattern': 'nginx',
      });
      expect(approved.note, '');
    });
  });

  group('ImageRollbackEntry', () {
    test('fromJson parses an ISO8601 capturedAt timestamp', () {
      final entry = ImageRollbackEntry.fromJson(const {
        'id': 'h1',
        'previousImage': 'nginx:1.26',
        'capturedAt': '2026-01-15T10:30:00Z',
      });
      expect(entry.previousImage, 'nginx:1.26');
      expect(entry.capturedAt, DateTime.parse('2026-01-15T10:30:00Z'));
    });
  });
}
