# Security Policy

resticctl handles repository credentials, executes configured commands, and
creates temporary database snapshots. Security reports are taken seriously.

## Supported versions

Security fixes are made on the `main` branch and released in the newest project
version. Older releases may not receive backports until a formal support policy
is announced.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private
[security advisory form](https://github.com/BharathKumarRavichandran/resticctl/security/advisories/new)
to report it to the maintainers.

Include the affected version or commit, operating system, impact, reproduction
steps, and any suggested mitigation. Do not include real repository passwords,
cloud credentials, private repository contents, or other user data. Use test
credentials and a disposable local repository when a proof of concept is
necessary.

The maintainers aim to acknowledge a report within seven days and provide an
initial assessment within fourteen days. Timelines for a fix and disclosure
depend on severity and complexity. Please allow time for a release before
publishing details.

## Security-sensitive areas

Changes involving password commands and files, credential environments,
subprocess arguments, temporary database snapshots, profile path validation,
file permissions, signal handling, or scheduler command generation require
focused security tests and review.
