import { connectMessageStream } from './message-stream';

const createEventSource = () => {
  const instances = [];

  class FakeEventSource {
    static CONNECTING = 0;
    static OPEN = 1;
    static CLOSED = 2;

    constructor(url, options) {
      this.url = url;
      this.options = options;
      this.readyState = FakeEventSource.CONNECTING;
      this.close = jest.fn(() => {
        this.readyState = FakeEventSource.CLOSED;
      });
      instances.push(this);
    }

    open() {
      this.readyState = FakeEventSource.OPEN;
      this.onopen();
    }

    emit(data) {
      this.onmessage({ data: JSON.stringify(data) });
    }

    fail(state = FakeEventSource.CONNECTING) {
      this.readyState = state;
      this.onerror();
    }
  }

  return { EventSourceImpl: FakeEventSource, instances };
};

describe('connectMessageStream', () => {
  afterEach(() => {
    jest.useRealTimers();
  });

  it('uses credentials, delivers events, and lets EventSource retry transient errors', () => {
    const { EventSourceImpl, instances } = createEventSource();
    const onMessage = jest.fn();
    const onOpen = jest.fn();
    const onError = jest.fn();
    const close = connectMessageStream('/api/message/stream', onMessage, {
      EventSourceImpl,
      onOpen,
      onError,
    });
    const stream = instances[0];

    expect(stream.options).toEqual({ withCredentials: true });
    stream.open();
    stream.emit({ id: 42, title: 'new message' });
    stream.fail(EventSourceImpl.CONNECTING);
    stream.fail(EventSourceImpl.CONNECTING);

    expect(onOpen).toHaveBeenCalledWith({ reconnected: false });
    expect(onMessage).toHaveBeenCalledWith({ id: 42, title: 'new message' });
    expect(onError).toHaveBeenCalledTimes(1);
    expect(instances).toHaveLength(1);

    close();
    expect(stream.close).toHaveBeenCalledTimes(1);
  });

  it('recreates a closed stream and cancels a pending retry when unmounted', () => {
    jest.useFakeTimers();
    const { EventSourceImpl, instances } = createEventSource();
    const close = connectMessageStream('/api/message/stream', jest.fn(), {
      EventSourceImpl,
      retryDelay: 1000,
    });

    instances[0].fail(EventSourceImpl.CLOSED);
    expect(instances).toHaveLength(1);
    jest.advanceTimersByTime(1000);
    expect(instances).toHaveLength(2);

    instances[1].fail(EventSourceImpl.CLOSED);
    close();
    jest.advanceTimersByTime(1000);
    expect(instances).toHaveLength(2);
  });
});
