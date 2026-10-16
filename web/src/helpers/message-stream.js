export function connectMessageStream(
  url,
  onMessage,
  {
    EventSourceImpl = EventSource,
    onOpen = () => {},
    onError = () => {},
    retryDelay = 3000,
  } = {}
) {
  let closed = false;
  let eventSource;
  let retryTimeout;
  let hasOpened = false;
  let errorReported = false;

  const connect = () => {
    if (closed) return;

    const currentEventSource = new EventSourceImpl(url, {
      withCredentials: true,
    });
    eventSource = currentEventSource;

    currentEventSource.onopen = () => {
      if (closed || eventSource !== currentEventSource) return;
      onOpen({ reconnected: hasOpened });
      hasOpened = true;
      errorReported = false;
    };

    currentEventSource.onmessage = (event) => {
      if (closed || eventSource !== currentEventSource) return;
      try {
        onMessage(JSON.parse(event.data));
      } catch {
        // Ignore malformed events and keep the stream available for later ones.
      }
    };

    currentEventSource.onerror = () => {
      if (closed || eventSource !== currentEventSource) return;
      if (!errorReported) {
        onError();
        errorReported = true;
      }

      // EventSource reconnects by itself for transient network failures. A
      // CLOSED source (such as an authentication rejection) needs a new object.
      if (currentEventSource.readyState === EventSourceImpl.CLOSED) {
        currentEventSource.close();
        if (retryTimeout === undefined) {
          retryTimeout = setTimeout(() => {
            retryTimeout = undefined;
            connect();
          }, retryDelay);
        }
      }
    };
  };

  connect();

  return () => {
    closed = true;
    if (retryTimeout !== undefined) clearTimeout(retryTimeout);
    eventSource?.close();
  };
}
