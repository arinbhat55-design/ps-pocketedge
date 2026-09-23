import 'package:app/models/deployment.dart';
import 'package:app/models/deployment_event.dart';
import 'package:app/models/deployment_request.dart';
import 'package:app/models/deployment_revision.dart';
import 'package:app/models/drift_report.dart';
import 'package:app/models/environment_policy.dart';
import 'package:flutter_test/flutter_test.dart';

DeploymentEvent _event(int id, String phase, {String service = ''}) =>
    DeploymentEvent(
      id: id,
      deploymentId: 'd1',
      phase: phase,
      message: '$phase-$id',
      service: service,
      createdAt: DateTime(2026),
    );

void main() {
  test('Deployment parses rollout, health, and scale fields', () {
    final d = Deployment.fromJson({
      'id': 'd1',
      'serverId': 's1',
      'phase': 'running',
      'scales': {'worker': 3},
      'updateStrategy': 'rolling',
      'autoRollback': true,
      'rollbackPlan': 'revert',
      'gitRef': 'release',
      'autoDeploy': true,
      'healthStatus': 'healthy',
      'currentRevision': 4,
      'createdAt': '2026-01-01T00:00:00Z',
      'updatedAt': '2026-01-01T00:00:00Z',
    });
    expect(d.scales, {'worker': 3});
    expect(d.updateStrategy, 'rolling');
    expect(d.autoRollback, isTrue);
    expect(d.healthStatus, 'healthy');
    expect(d.currentRevision, 4);
    expect(d.metadata.toJson()['gitRef'], 'release');
  });

  test('DeploymentDetail parses requests, policy, and rollback target', () {
    final detail = DeploymentDetail.fromJson({
      'id': 'd1',
      'serverId': 's1',
      'createdAt': '2026-01-01T00:00:00Z',
      'updatedAt': '2026-01-01T00:00:00Z',
      'events': [],
      'openRequests': [
        {
          'id': 'r1',
          'deploymentId': 'd1',
          'action': 'rollback',
          'params': {'revision': 2},
          'status': 'pending_approval',
          'requestedAt': '2026-01-01T00:00:00Z',
        },
      ],
      'policy': {
        'environment': 'production',
        'requireApproval': true,
        'allowSelfApproval': false,
      },
      'rollbackTarget': {
        'revision': 2,
        'action': 'deploy',
        'status': 'healthy',
        'createdAt': '2026-01-01T00:00:00Z',
      },
      'serviceNames': ['web'],
    });
    expect(detail.openRequests.single.summary, 'Roll back to revision 2');
    expect(detail.policy!.ruleLabels, contains('Approval by another admin'));
    expect(detail.rollbackTarget!.revision, 2);
    expect(detail.serviceNames, ['web']);
  });

  test('isInProgress/isTerminal ignore per-service events', () {
    expect(_event(1, 'pulling').isInProgress, isTrue);
    expect(_event(2, 'verifying').isInProgress, isTrue);
    expect(_event(3, 'healthy').isTerminal, isTrue);
    expect(_event(4, 'running', service: 'web').isTerminal, isFalse);
    expect(_event(5, 'running', service: 'web').isInProgress, isFalse);
  });

  test('latestServiceProgress only covers the most recent rollout', () {
    final events = [
      _event(1, 'pending'),
      _event(2, 'running', service: 'old'),
      _event(3, 'pending'),
      _event(4, 'pulling', service: 'db'),
      _event(5, 'pulling', service: 'web'),
      _event(6, 'running', service: 'db'),
    ];
    final progress = latestServiceProgress(events);
    expect(progress.map((p) => p.service), ['db', 'web']);
    expect(progress.first.phase, 'running');
  });

  test('DeploymentActionOutcome describes queued outcomes', () {
    final queued = DeploymentActionOutcome.fromJson({
      'deploymentId': 'd1',
      'status': 'pending_approval',
      'message': 'the production environment requires approval',
    });
    expect(queued.isQueued, isTrue);
    expect(queued.describe(), contains('Waiting for approval'));
  });

  test('MaintenanceWindow round-trips and describes itself', () {
    final w = MaintenanceWindow.fromJson({
      'days': [6, 0],
      'start': '22:00',
      'end': '02:00',
    });
    expect(w.toJson(), {
      'days': [6, 0],
      'start': '22:00',
      'end': '02:00',
    });
    expect(w.describe(), 'Sun, Sat 22:00–02:00 UTC');
  });

  test('DeploymentRevision rollback candidates', () {
    DeploymentRevision rev(String status) => DeploymentRevision.fromJson({
      'revision': 1,
      'action': 'deploy',
      'status': status,
      'createdAt': '2026-01-01T00:00:00Z',
    });
    expect(rev('healthy').isRollbackCandidate, isTrue);
    expect(rev('rolled_back').isRollbackCandidate, isFalse);
    expect(rev('failed').isRollbackCandidate, isFalse);
  });

  test('DriftReport parses all three sections', () {
    final r = DriftReport.fromJson({
      'checkedAt': '2026-01-01T00:00:00Z',
      'drifted': true,
      'config': {'drifted': true, 'reason': 'changed'},
      'git': {'drifted': true, 'ref': 'main', 'latestCommit': 'abc'},
      'runtime': {
        'drifted': true,
        'missing': ['pe-d-web-2'],
        'notRunning': [],
      },
    });
    expect(r.config.reason, 'changed');
    expect(r.git!.ref, 'main');
    expect(r.runtime.missing, ['pe-d-web-2']);
  });

  test('DeploymentRequest summaries', () {
    DeploymentRequest req(String action, Map<String, dynamic> params) =>
        DeploymentRequest.fromJson({
          'id': 'r',
          'deploymentId': 'd',
          'action': action,
          'params': params,
          'status': 'scheduled',
          'requestedAt': '2026-01-01T00:00:00Z',
        });
    expect(
      req('scale', {'service': 'web', 'replicas': 3}).summary,
      'Scale web to 3',
    );
    expect(
      req('rollback', {'gitCommit': '0123456789abcdef'}).summary,
      'Roll back to commit 0123456',
    );
    expect(
      req('deploy', {'fromDeploymentId': 'x', 'fromRevision': 5}).summary,
      'Promote (revision 5)',
    );
  });
}
