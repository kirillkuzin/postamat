# Governance

postamat is maintained as an open-source project under the MIT license.

## Roles

- **Maintainers** steward the architecture, review pull requests, triage issues, manage releases, and make final project decisions.
- **Contributors** propose issues, documentation, tests, code, deployment recipes, and design improvements.
- **Security reporters** privately disclose vulnerabilities and coordinate fixes with maintainers.

## Decision making

The project favors transparent technical discussion in issues and pull requests. Maintainers make final decisions based on:

- security and privacy impact;
- architectural consistency with the control-plane/data-plane separation;
- maintainability and test coverage;
- self-hosting practicality;
- contributor and user feedback.

Large design changes should start as an issue before implementation.

## Review policy

Pull requests require maintainer review before merge. Security-sensitive changes may require additional review, focused tests, or a private security discussion before public merge.

## Releases

Until stable versioned releases begin, the default branch is the integration branch. A release policy with semantic versioning, changelog, signed artifacts, and supported security branches will be added before the first stable release.
