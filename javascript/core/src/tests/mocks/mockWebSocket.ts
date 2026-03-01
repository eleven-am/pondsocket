export class MockWebSocket {
    readyState = 1;

    on = jest.fn();

    send = jest.fn();

    close = jest.fn();
}
