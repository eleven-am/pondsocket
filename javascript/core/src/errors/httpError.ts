export class HttpError extends Error {
    readonly statusText: string;

    constructor (public readonly statusCode: number, message: string) {
        super(message);
        this.name = 'HttpError';
        this.statusText = HttpError.getStatusText(statusCode);
    }

    static getStatusText (statusCode: number) {
        switch (statusCode) {
            case 400:
                return 'Bad Request';
            case 401:
                return 'Unauthorized';
            case 403:
                return 'Forbidden';
            case 404:
                return 'Not Found';
            case 405:
                return 'Method Not Allowed';
            case 406:
                return 'Not Acceptable';
            case 409:
                return 'Conflict';
            case 429:
                return 'Too Many Requests';
            case 500:
                return 'Internal Server Error';
            case 501:
                return 'Not Implemented';
            case 502:
                return 'Bad Gateway';
            case 503:
                return 'Service Unavailable';
            case 504:
                return 'Gateway Timeout';
            default:
                return 'Unknown Error';
        }
    }
}
