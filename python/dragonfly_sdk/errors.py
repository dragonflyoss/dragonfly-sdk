"""Exceptions raised by the Dragonfly Python SDK."""


class DragonflyError(Exception):
    """Base class for all errors raised by the Dragonfly SDK."""


class DownloadError(DragonflyError):
    """Raised when a dfdaemon download task fails."""
