import { STATUS_CODES } from 'node:http';
import { Duplex } from 'node:stream';

export function rejectUpgrade (socket: Duplex, requestedStatus: number, message: string): void {
    const status = Number.isInteger(requestedStatus) && requestedStatus >= 400 && requestedStatus <= 599
        ? requestedStatus
        : 500;
    const reason = STATUS_CODES[status] ?? 'Error';
    const body = message || reason;

    try {
        socket.write(
            `HTTP/1.1 ${status} ${reason}\r\n` +
            'Connection: close\r\n' +
            'Content-Type: text/plain; charset=utf-8\r\n' +
            `Content-Length: ${Buffer.byteLength(body)}\r\n` +
            `\r\n${body}`,
        );
    } finally {
        socket.destroy();
    }
}
