export class MockWebSocket {
    on = jest.fn();

    send = jest.fn();

    close = jest.fn();
}
