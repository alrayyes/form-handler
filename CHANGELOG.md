# Changelog

Written by the release job, not by hand. Every entry below comes from the
Conventional Commits that landed on `master`: `feat:` takes the minor, `fix:`
the patch, a `BREAKING CHANGE:` footer the major, and a run of only `docs:` and
`chore:` releases nothing at all.

Editing this file yourself is fine for fixing a typo, but the next release will
insert its section under the marker below and leave whatever else it finds
alone.

<!-- releases below -->

## v1.3.2

### Fixes
* 523b32722bfe3f043ac2f459268fa2099ec78b79 fix(build): report the same version from the binary and the image
### Everything else
* b31720701571dee5c29984c39274c028e4a068a6 Merge pull request #11 from alrayyes/fix/version-string

## v1.3.1

### Fixes
* 5654f1f501fd17ebbae80cdade767d734ffcdd79 fix(security): strip line breaks by name so the sanitiser is recognised
### Everything else
* e332d49ce22e29a8e6630adb11877753b078494f Merge pull request #9 from alrayyes/fix/logsafe-recognised

## v1.3.0

### Features
* 2dcd94575334674d4f5b98929e7739b0ced4b641 feat: log refused submissions, not just accepted ones
### Fixes
* 44240b3cba59646dd4089fb55a0c2ba47c4c898c fix(security): clean every user-supplied value that reaches a log
* a3d0a05674366299bdfe05b8fd808f3087eedcc2 fix(security): strip line breaks from logged values
### Everything else
* 48f865e536b758ca16b0ba6ae6705b116fd62799 Merge pull request #8 from alrayyes/feat/log-refusals

## v1.2.0

### Features
* cd24215ba18231398ecbe979c4062d76c728c99d feat(cli): parse arguments with cobra
* a97ee000356c5009d501950913da3f7dea70fd4c feat: send through Mailgun as well as SMTP
### Fixes
* cef0a72a4e7bb71bf1343ed8c079d1d8c8d58301 fix(cli): report a failure once rather than twice
### Everything else
* ba42568604ef7a557a6eecf829ef80bdcaeec97e Merge pull request #4 from alrayyes/feat/cobra-cli
* d3276ed0787adbb4ed77a2f123df9e5308e98ae5 Merge pull request #5 from alrayyes/feat/mailgun
* 052fcb468d74cc4996624d399acdfa79e8b869f1 ci: lint the integration tests as well
* 836d79f08fd88ab1d7372de7d297157225a20bdb test(cli): pin the single-dash flags against a real probe

## v1.1.0

### Features
* 39fe69a089a5b17f2d50986968cdf0e6e2d46dcf feat: give every form its own mail server login
### Fixes
* 8e46e2f8aa2b41354a47a57749d4211f2598364f fix(ci): give the changelog commit a key it can push with
### Everything else
* f6b036b659d4cf617aac6995a796edea9567b2de docs: record v1.0.0 in the changelog, and drop a retired badge

## v1.0.0

### Features
* 6ad307816a81c3b241bc8f1c372b8dbcb7c8c8ff feat!: serve any number of forms across any number of domains
* 478a18b082251817d02b6b7b629f97997239d6e5 feat: accept the contact form and send it as email
* ad1f6b937ac4b11c5c5922f917bac4834fc98d1f feat: probe itself with -healthcheck
* decaaba3a8f28dd0ba86ca0ca5688efee0a82139 feat: report the version it was built from
### Fixes
* 125897027dd28c188a17172714369a1ba678683d fix(ci): build the image with buildah, and build it on branches too
* d7c74d5eb915e05b8c14d8841c1c6a31083ec614 fix(ci): build the image with ko, and drop the Dockerfile
* 8b8dd517f6dea23677b9bb3d4b189e77665a0b71 fix(ci): let the release job fetch the toolchain svu needs
* e0800b09721ef396c2c6f42021641bd39424d736 fix(ci): run the integration test without a Docker daemon in the job
* 0262513b2ca201fa057ed8ac6a6d69a5281c7013 fix(ci): stop linting the linters' own READMEs
* 31f566078d52e11d5cf0fddd97fe73950d8bc401 fix(ci): target Go 1.25 rather than the toolchain that happened to be installed
* 43263859f656bba792c3caeff4932bbad868e408 fix: make the commit-msg hook able to run at all
### Everything else
* 745411273dd4500a5eb06fd184edfbea289e6490 build: format the Markdown with Prettier before linting it
* d6f79298afe03771a4eb521ade63a8bfd6cc2663 build: keep the release output out of the build context
* 7f7c6a3caeabcde0d8b9a1a53a4369c654c3a677 build: pin the mail server image by digest
* bb9ef0710aa57b8d298a5d074597dc9926164a9a chore: let Renovate see the pins it cannot find on its own
* b7c9b74a3ea130d682cf7f27a0a012625126c5f6 ci: build, test and analyse on GitHub Actions
* 694aa64e939724ad1b53b6a643a70c643b1b7b98 ci: check the whole branch's commit messages
* 8d6839d4bafacf6edfd9d862c6f7fd1ed45f046d ci: let Dependabot keep the dependencies current
* f6cb744f1be438b663bab3a257f96c9d991f1059 ci: lint the Markdown this repo owns
* 8171223bfb048e6a36d165a9237015cbcf320309 ci: name the analysis jobs after what they analyse
* ce59e47ec9ad33e772b579b0ead3d9775c0b4106 ci: pause releases until there is a token to make them with
* 9ac980a243242f6b3937301073d91f1584af1016 ci: release itself from the commits that landed
* 221fbd190aebfd2a63f4b0533ba193ed2c76a036 ci: release itself from the commits that landed
* 951c1a3847a6860716b18615aae25b66ff5aca32 ci: run the golangci-lint version people actually have
* 8dea76f9ca21cee8d332d66751a7555b00d386e9 docs: describe the multi-form service this has become
* 73a30b41ae74b3dc1996e3e290ce8cabecc1b8e1 docs: give the README the sections a newcomer needs
* 7c59558e5582927a572cea5f4d2198d08c6b60d3 docs: license the code under GPL-3.0-or-later
* 0a94c69e687b8f5863c074ec06467cde22910661 docs: say how to contribute, and how to report a hole in it
* 8e7aaf4ec4218ff758373e97da0c7dae934a29ee docs: show Validate's contract as runnable examples
