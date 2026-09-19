#!/usr/bin/env python3
import argparse
import getpass
import os
import re
import sys

from credential_store import (
    CredentialStoreError,
    PASSWORD_NOT_FOUND,
    list_users,
    set_password,
)


DEFAULT_USERS_FILE = os.environ.get(
    "SCREEGO_USERS_FILE_HOST", "/etc/gamecast-sfu/users/users.htpasswd"
)
USERNAME_RE = re.compile(r"^[a-zA-Z0-9_-]{2,20}$")


def build_parser():
    parser = argparse.ArgumentParser(description="Manage partylan server users")
    parser.add_argument("--users-file", default=DEFAULT_USERS_FILE, help="Path to users.htpasswd")
    commands = parser.add_subparsers(dest="command")
    commands.add_parser("list", help="List registered usernames")
    set_parser = commands.add_parser("set-password", help="Reset an existing user's password")
    set_parser.add_argument("username")
    return parser


def run(argv=None, password_reader=getpass.getpass, stdout=sys.stdout, stderr=sys.stderr):
    parser = build_parser()
    args = parser.parse_args(argv)
    if not args.command:
        parser.print_usage(stderr)
        return 2
    try:
        if args.command == "list":
            for username in list_users(args.users_file):
                print(username, file=stdout)
            return 0

        username = args.username.strip()
        if not USERNAME_RE.fullmatch(username):
            print("用户名只能包含字母、数字、下划线、横线，2-20字符", file=stderr)
            return 2
        password = password_reader("新密码: ")
        confirmation = password_reader("再次输入新密码: ")
        if password != confirmation:
            print("两次输入的密码不一致", file=stderr)
            return 2
        if len(password) < 4:
            print("密码至少4个字符", file=stderr)
            return 2
        if set_password(args.users_file, username, password) == PASSWORD_NOT_FOUND:
            print("用户不存在: %s" % username, file=stderr)
            return 3
        print("密码已更新: %s" % username, file=stdout)
        return 0
    except CredentialStoreError as error:
        print("用户文件更新失败: %s" % error, file=stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(run())
