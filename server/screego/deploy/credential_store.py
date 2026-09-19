import contextlib
import os
import stat
import tempfile
import threading

import bcrypt


CREATE_CREATED = "created"
CREATE_EXISTS = "exists"
PASSWORD_UPDATED = "updated"
PASSWORD_NOT_FOUND = "not_found"

_thread_lock = threading.RLock()


class CredentialStoreError(Exception):
    pass


def _record_matches(line, username):
    return line.startswith(username.encode("utf-8") + b":")


def _read(path):
    if not os.path.exists(path):
        return b"", 0o600
    with open(path, "rb") as users_file:
        content = users_file.read()
    return content, stat.S_IMODE(os.stat(path).st_mode)


def _lock_handle(handle):
    if os.name == "nt":
        import msvcrt

        handle.seek(0, os.SEEK_END)
        if handle.tell() == 0:
            handle.write(b"\0")
            handle.flush()
        handle.seek(0)
        msvcrt.locking(handle.fileno(), msvcrt.LK_LOCK, 1)
        return lambda: msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)

    import fcntl

    fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
    return lambda: fcntl.flock(handle.fileno(), fcntl.LOCK_UN)


@contextlib.contextmanager
def _locked(path):
    directory = os.path.dirname(os.path.abspath(path))
    os.makedirs(directory, exist_ok=True)
    lock_path = path + ".lock"
    with _thread_lock:
        with open(lock_path, "a+b") as lock_file:
            if hasattr(os, "fchmod"):
                os.fchmod(lock_file.fileno(), 0o600)
            unlock = _lock_handle(lock_file)
            try:
                yield
            finally:
                unlock()


def _fsync_directory(directory):
    try:
        directory_fd = os.open(directory, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(directory_fd)
    except OSError:
        pass
    finally:
        os.close(directory_fd)


def _atomic_replace(path, content, file_mode):
    directory = os.path.dirname(os.path.abspath(path))
    file_descriptor = None
    temp_path = None
    try:
        file_descriptor, temp_path = tempfile.mkstemp(
            dir=directory,
            prefix=".%s." % os.path.basename(path),
            suffix=".tmp",
        )
        if hasattr(os, "fchmod"):
            os.fchmod(file_descriptor, file_mode)
        with os.fdopen(file_descriptor, "wb") as temp_file:
            file_descriptor = None
            temp_file.write(content)
            temp_file.flush()
            os.fsync(temp_file.fileno())
        if not hasattr(os, "fchmod"):
            os.chmod(temp_path, file_mode)
        os.replace(temp_path, path)
        temp_path = None
        _fsync_directory(directory)
    except Exception as error:
        raise CredentialStoreError(type(error).__name__) from error
    finally:
        if file_descriptor is not None:
            os.close(file_descriptor)
        if temp_path:
            try:
                os.unlink(temp_path)
            except OSError:
                pass


def list_users(path):
    with _locked(path):
        content, _file_mode = _read(path)
    users = []
    seen = set()
    for line in content.splitlines():
        if not line or line.startswith(b"#") or b":" not in line:
            continue
        try:
            username = line.split(b":", 1)[0].decode("utf-8")
        except UnicodeDecodeError:
            continue
        if username and username not in seen:
            seen.add(username)
            users.append(username)
    return users


def create_user(path, username, password, rounds=12):
    with _locked(path):
        content, file_mode = _read(path)
        if any(_record_matches(line, username) for line in content.splitlines()):
            return CREATE_EXISTS
        password_hash = bcrypt.hashpw(password.encode("utf-8"), bcrypt.gensalt(rounds=rounds))
        separator = b"" if not content or content.endswith(b"\n") else b"\n"
        updated = content + separator + username.encode("utf-8") + b":" + password_hash + b"\n"
        _atomic_replace(path, updated, file_mode)
        return CREATE_CREATED


def set_password(path, username, password, rounds=12):
    with _locked(path):
        content, file_mode = _read(path)
        lines = content.splitlines(keepends=True)
        if not any(_record_matches(line.rstrip(b"\r\n"), username) for line in lines):
            return PASSWORD_NOT_FOUND

        password_hash = bcrypt.hashpw(password.encode("utf-8"), bcrypt.gensalt(rounds=rounds))
        prefix = username.encode("utf-8") + b":" + password_hash
        updated_lines = []
        for line in lines:
            body = line.rstrip(b"\r\n")
            ending = line[len(body) :]
            updated_lines.append(prefix + ending if _record_matches(body, username) else line)
        _atomic_replace(path, b"".join(updated_lines), file_mode)
        return PASSWORD_UPDATED
