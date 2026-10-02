import 'package:app/api/api_client.dart';
import 'package:app/features/containers/create_container_dialog.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('Cancel closes create container without submitting', (
    tester,
  ) async {
    tester.view.physicalSize = const Size(900, 900);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);

    String? result = 'not closed';
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Builder(
            builder: (context) => FilledButton(
              onPressed: () async {
                result = await showCreateContainerDialog(
                  context,
                  apiClient: ApiClient(baseUrl: 'http://localhost:8080'),
                  serverId: 'server-1',
                  serverName: 'Local',
                );
              },
              child: const Text('Open create form'),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('Open create form'));
    await tester.pumpAndSettle();
    final cancel = find.widgetWithText(OutlinedButton, 'Cancel');
    await tester.ensureVisible(cancel);
    await tester.tap(cancel);
    await tester.pumpAndSettle();

    expect(find.text('Create container'), findsNothing);
    expect(result, isNull);
  });
}
