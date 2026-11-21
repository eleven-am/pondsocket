export class MockWebSocket {
    readyState = 1;

    bufferedAmount = 0;

    on = jest.fn();

    send = jest.fn();

    close = jest.fn();

    terminate = jest.fn();
}
