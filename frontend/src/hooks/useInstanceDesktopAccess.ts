import { useCallback, useEffect, useRef, useState } from "react";
import { instanceService } from "../services/instanceService";

interface RefreshAccessOptions {
  forceReload?: boolean;
  silent?: boolean;
}

interface UseInstanceDesktopAccessOptions {
  instanceId: number | null;
  isRunning: boolean;
  retainSessionOnStop?: boolean;
  resolveEmbedUrl: (url: string | null) => string | null;
  failedMessage: string;
  refreshLeadMs?: number;
  retryDelayMs?: number;
}

const DEFAULT_REFRESH_LEAD_MS = 5 * 60 * 1000;
const DEFAULT_RETRY_DELAY_MS = 5000;
const MAX_RETRY_DELAY_MS = 30 * 1000;
const RETRY_JITTER_RATIO = 0.25;
const FRAME_ERROR_PATTERN =
  /(access token expired or invalid|access token required|token does not match instance|failed to proxy request)/i;

type DesktopSessionSnapshot = {
  embedUrl: string | null;
  expiresAt: number | null;
  hasEstablishedSession: boolean;
};

type DesktopAccessResponse = Awaited<
  ReturnType<typeof instanceService.generateAccessToken>
>;

const desktopSessionStore = new Map<number, DesktopSessionSnapshot>();
const accessRequestStore = new Map<number, Promise<DesktopAccessResponse>>();

function requestDesktopAccess(
  instanceId: number,
): Promise<DesktopAccessResponse> {
  const existingRequest = accessRequestStore.get(instanceId);
  if (existingRequest) {
    return existingRequest;
  }

  const request = instanceService.generateAccessToken(instanceId);
  const trackedRequest = request.finally(() => {
    if (accessRequestStore.get(instanceId) === trackedRequest) {
      accessRequestStore.delete(instanceId);
    }
  });

  accessRequestStore.set(instanceId, trackedRequest);
  return trackedRequest;
}

export function useInstanceDesktopAccess({
  instanceId,
  isRunning,
  retainSessionOnStop = false,
  resolveEmbedUrl,
  failedMessage,
  refreshLeadMs = DEFAULT_REFRESH_LEAD_MS,
  retryDelayMs = DEFAULT_RETRY_DELAY_MS,
}: UseInstanceDesktopAccessOptions) {
  const initialCachedSession = instanceId
    ? desktopSessionStore.get(instanceId)
    : null;

  const [embedUrl, setEmbedUrl] = useState<string | null>(
    initialCachedSession?.embedUrl ?? null,
  );
  const [expiresAt, setExpiresAt] = useState<Date | null>(
    initialCachedSession?.expiresAt
      ? new Date(initialCachedSession.expiresAt)
      : null,
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [reconnecting, setReconnecting] = useState(false);

  const requestIdRef = useRef(0);
  const embedUrlRef = useRef<string | null>(initialCachedSession?.embedUrl ?? null);
  const expiresAtRef = useRef<Date | null>(
    initialCachedSession?.expiresAt
      ? new Date(initialCachedSession.expiresAt)
      : null,
  );
  const retryTimeoutRef = useRef<number | null>(null);
  const refreshTimeoutRef = useRef<number | null>(null);
  const retryAttemptRef = useRef(0);
  const refreshAccessRef = useRef<
    ((options?: RefreshAccessOptions) => Promise<void>) | null
  >(null);
  const hasEstablishedSessionRef = useRef(
    initialCachedSession?.hasEstablishedSession ?? false,
  );

  const syncSessionStore = useCallback(
    (patch: Partial<DesktopSessionSnapshot>) => {
      if (!instanceId) {
        return;
      }

      const current = desktopSessionStore.get(instanceId) ?? {
        embedUrl: embedUrlRef.current,
        expiresAt: expiresAtRef.current?.getTime() ?? null,
        hasEstablishedSession: hasEstablishedSessionRef.current,
      };

      desktopSessionStore.set(instanceId, {
        embedUrl:
          patch.embedUrl !== undefined ? patch.embedUrl : current.embedUrl,
        expiresAt:
          patch.expiresAt !== undefined ? patch.expiresAt : current.expiresAt,
        hasEstablishedSession:
          patch.hasEstablishedSession !== undefined
            ? patch.hasEstablishedSession
            : current.hasEstablishedSession,
      });
    },
    [instanceId],
  );

  useEffect(() => {
    embedUrlRef.current = embedUrl;
    syncSessionStore({ embedUrl });
  }, [embedUrl, syncSessionStore]);

  useEffect(() => {
    expiresAtRef.current = expiresAt;
    syncSessionStore({ expiresAt: expiresAt?.getTime() ?? null });
  }, [expiresAt, syncSessionStore]);

  const clearRetryTimeout = useCallback(() => {
    if (retryTimeoutRef.current !== null) {
      window.clearTimeout(retryTimeoutRef.current);
      retryTimeoutRef.current = null;
    }
  }, []);

  const clearRefreshTimeout = useCallback(() => {
    if (refreshTimeoutRef.current !== null) {
      window.clearTimeout(refreshTimeoutRef.current);
      refreshTimeoutRef.current = null;
    }
  }, []);

  const clearAccessState = useCallback(() => {
    clearRetryTimeout();
    clearRefreshTimeout();
    requestIdRef.current += 1;
    embedUrlRef.current = null;
    expiresAtRef.current = null;
    hasEstablishedSessionRef.current = false;
    retryAttemptRef.current = 0;
    if (instanceId) {
      desktopSessionStore.delete(instanceId);
    }
    setEmbedUrl(null);
    setExpiresAt(null);
    setError(null);
    setLoading(false);
    setReconnecting(false);
  }, [clearRefreshTimeout, clearRetryTimeout, instanceId]);

  useEffect(() => {
    clearRetryTimeout();
    clearRefreshTimeout();
    requestIdRef.current += 1;

    const cachedSession = instanceId
      ? desktopSessionStore.get(instanceId)
      : null;
    const nextEmbedUrl = cachedSession?.embedUrl ?? null;
    const nextExpiresAt = cachedSession?.expiresAt
      ? new Date(cachedSession.expiresAt)
      : null;

    embedUrlRef.current = nextEmbedUrl;
    expiresAtRef.current = nextExpiresAt;
    hasEstablishedSessionRef.current =
      cachedSession?.hasEstablishedSession ?? false;
    retryAttemptRef.current = 0;

    setEmbedUrl(nextEmbedUrl);
    setExpiresAt(nextExpiresAt);
    setError(null);
    setLoading(false);
    setReconnecting(false);
  }, [clearRefreshTimeout, clearRetryTimeout, instanceId]);

  const shouldPreserveSession = useCallback(() => {
    return (
      retainSessionOnStop &&
      (hasEstablishedSessionRef.current || Boolean(embedUrlRef.current))
    );
  }, [retainSessionOnStop]);

  const scheduleRetry = useCallback(() => {
    clearRetryTimeout();
    if (shouldPreserveSession()) {
      return;
    }
    if (!instanceId || !isRunning || document.hidden) {
      return;
    }

    const attempt = retryAttemptRef.current + 1;
    retryAttemptRef.current = attempt;
    const exponentialDelay = Math.min(
      retryDelayMs * 2 ** (attempt - 1),
      MAX_RETRY_DELAY_MS,
    );
    const jitterWindow = Math.floor(exponentialDelay * RETRY_JITTER_RATIO);
    const nextDelay = Math.max(
      retryDelayMs,
      exponentialDelay -
        jitterWindow +
        Math.floor(Math.random() * (jitterWindow * 2 + 1)),
    );

    retryTimeoutRef.current = window.setTimeout(() => {
      retryTimeoutRef.current = null;
      void refreshAccessRef.current?.({
        forceReload: !embedUrlRef.current,
        silent: true,
      });
    }, nextDelay);
  }, [clearRetryTimeout, instanceId, isRunning, retryDelayMs, shouldPreserveSession]);

  const refreshAccess = useCallback(
    async ({ forceReload = false, silent = false }: RefreshAccessOptions = {}) => {
      if (!instanceId || !isRunning) {
        if (shouldPreserveSession()) {
          return;
        }
        clearAccessState();
        return;
      }

      const requestId = requestIdRef.current + 1;
      requestIdRef.current = requestId;
      clearRetryTimeout();

      if (silent) {
        setReconnecting(true);
      } else {
        setLoading(true);
      }

      try {
        const data = await requestDesktopAccess(instanceId);
        if (requestId !== requestIdRef.current) {
          return;
        }

        const nextEmbedUrl = resolveEmbedUrl(data.access_url || data.proxy_url);
        const nextExpiresAt = new Date(data.expires_at);
        const previousEmbedUrl = embedUrlRef.current;

        expiresAtRef.current = nextExpiresAt;
        retryAttemptRef.current = 0;
        setExpiresAt(nextExpiresAt);
        setError(null);

        if (!previousEmbedUrl || forceReload) {
          embedUrlRef.current = nextEmbedUrl;
          setEmbedUrl(nextEmbedUrl);
        } else {
          setEmbedUrl(previousEmbedUrl);
        }

        syncSessionStore({
          embedUrl: !previousEmbedUrl || forceReload ? nextEmbedUrl : previousEmbedUrl,
          expiresAt: nextExpiresAt.getTime(),
        });
      } catch (err: any) {
        if (requestId !== requestIdRef.current) {
          return;
        }

        setError(err.response?.data?.error || failedMessage);
        if (!embedUrlRef.current) {
          setEmbedUrl(null);
          setExpiresAt(null);
        }
        scheduleRetry();
      } finally {
        if (requestId === requestIdRef.current) {
          setLoading(false);
          setReconnecting(false);
        }
      }
    },
    [
      clearAccessState,
      clearRetryTimeout,
      failedMessage,
      instanceId,
      isRunning,
      resolveEmbedUrl,
      scheduleRetry,
      shouldPreserveSession,
      syncSessionStore,
    ],
  );

  useEffect(() => {
    refreshAccessRef.current = refreshAccess;
  }, [refreshAccess]);

  useEffect(() => {
    if (!instanceId) {
      clearAccessState();
      return;
    }

    if (!isRunning) {
      if (shouldPreserveSession() || embedUrlRef.current) {
        clearRetryTimeout();
        clearRefreshTimeout();
        return;
      }

      clearAccessState();
      return;
    }

    void refreshAccess({ forceReload: true });

    return () => {
      clearRetryTimeout();
      clearRefreshTimeout();
    };
  }, [
    clearAccessState,
    clearRefreshTimeout,
    clearRetryTimeout,
    instanceId,
    isRunning,
    refreshAccess,
    shouldPreserveSession,
  ]);

  useEffect(() => {
    if (!instanceId || !isRunning || !expiresAt) {
      clearRefreshTimeout();
      return;
    }

    if (shouldPreserveSession()) {
      clearRefreshTimeout();
      return;
    }

    const remainingMs = expiresAt.getTime() - Date.now();
    const delay =
      remainingMs <= refreshLeadMs
        ? Math.max(remainingMs - 30 * 1000, 0)
        : remainingMs - refreshLeadMs;

    refreshTimeoutRef.current = window.setTimeout(() => {
      refreshTimeoutRef.current = null;
      void refreshAccessRef.current?.({ silent: true });
    }, delay);

    return clearRefreshTimeout;
  }, [
    clearRefreshTimeout,
    expiresAt,
    instanceId,
    isRunning,
    refreshLeadMs,
    shouldPreserveSession,
  ]);

  useEffect(() => {
    if (shouldPreserveSession()) {
      return;
    }

    const maybeReconnect = () => {
      if (!instanceId || !isRunning) {
        return;
      }

      const currentExpiry = expiresAtRef.current?.getTime() ?? 0;
      const hasActiveFrame = Boolean(embedUrlRef.current);
      const isNearExpiry =
        currentExpiry === 0 || currentExpiry - Date.now() <= refreshLeadMs;

      if (!hasActiveFrame) {
        void refreshAccessRef.current?.({ forceReload: true, silent: true });
        return;
      }

      if (isNearExpiry) {
        void refreshAccessRef.current?.({ silent: true });
      }
    };

    const handleVisibilityChange = () => {
      if (!document.hidden) {
        maybeReconnect();
      }
    };

    const handleFocus = () => {
      maybeReconnect();
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    window.addEventListener("focus", handleFocus);

    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.removeEventListener("focus", handleFocus);
    };
  }, [instanceId, isRunning, refreshLeadMs, shouldPreserveSession]);

  const handleFrameLoad = useCallback(
    (frame: HTMLIFrameElement | null) => {
      if (!frame) {
        return;
      }

      window.setTimeout(() => {
        try {
          const frameText = frame.contentDocument?.body?.textContent?.trim() ?? "";
          if (frameText && FRAME_ERROR_PATTERN.test(frameText)) {
            if (!hasEstablishedSessionRef.current) {
              void refreshAccessRef.current?.({ forceReload: true, silent: true });
            }
            return;
          }

          hasEstablishedSessionRef.current = true;
          syncSessionStore({ hasEstablishedSession: true });
        } catch (frameError) {
          console.error("Failed to inspect desktop frame state", frameError);
        }
      }, 0);
    },
    [syncSessionStore],
  );

  const handleFrameError = useCallback(() => {
    if (!hasEstablishedSessionRef.current) {
      void refreshAccessRef.current?.({ forceReload: true, silent: true });
    }
  }, []);

  return {
    embedUrl,
    expiresAt,
    loading,
    error,
    reconnecting,
    refreshAccess,
    handleFrameLoad,
    handleFrameError,
  };
}
