---
name: release
description: gh-orbit 전용 /release 스킬. `v<x.y.z>` 태그 push → `.github/workflows/release.yml` → GoReleaser 가 darwin/linux/windows × amd64/arm64 바이너리를 발행하고 사용자가 `gh extension install jeonbyeongmin/gh-orbit` 로 받는 인프라를 호출한다. 사용자가 "릴리즈", "release", "버전 발행", "태그 push" 같은 의도를 보이면 즉시 트리거. 인자 한 개(`v0.1.0` 또는 `0.1.0`)를 받으며, 인자가 비면 latest tag 보여주고 다음 버전을 묻는다. 검증(default 브랜치/clean tree/CI green) → goreleaser snapshot dry-run → 사용자 확인 → tag push → Actions 모니터링 → 자산 검증을 한 호흡으로 수행. destructive 단계(`git push <tag>`) 전 반드시 사용자 확인. 검증 실패는 우회하지 않는다.
---

# /release (gh-orbit project-local)

이 스킬은 gh-orbit 의 GitHub Release + GoReleaser 인프라를 호출해 `gh extension install jeonbyeongmin/gh-orbit` 가 동작하는 릴리즈를 발행한다. 인프라는 두 개로 분리돼 있다:

- `.github/workflows/release.yml` — `v*` 태그 push 시 자동 동작
- `.goreleaser.yml` — darwin/linux/windows × amd64/arm64 (windows arm64 제외) 바이너리 + 이름 템플릿

스킬의 역할은 **태그를 안전하게 push 하고 그 결과를 끝까지 책임지는 것**이다. 단순히 `git tag && git push` 가 아니다 — 사전 검증, dry-run 으로 빌드 인프라가 깨지지 않았는지 확인, push 후 워크플로우 모니터링, release 자산이 `gh extension install` 호환 패턴인지 검증까지 포함한다.

원칙:

- **destructive 단계는 반드시 사용자 확인.** `git push <tag>` 는 한 번 push 되면 GitHub 에 release 가 만들어지고 자산이 첨부된다. 잘못된 태그는 사용자가 직접 정리해야 하므로 사전 확인이 필수.
- **검증 실패는 우회하지 않는다.** CI red, dirty tree, default 브랜치 아님 같은 신호는 모두 중단 사유다. `--force` 류 옵션은 자동으로 쓰지 않는다.
- **gh extension lookup 호환성은 인프라의 정합성 체크다.** 자산 이름 패턴이 cli/cli 가 인식하는 형식과 다르면 release 가 만들어져도 사용자는 설치하지 못한다 — dry-run 단계와 사후 검증에서 두 번 확인한다.

## 0. 도구 사전 로드

destructive 분기 직전에 사용자에게 묻는다. 시작 시:

```
ToolSearch(query="select:AskUserQuestion", max_results=1)
```

## 1. 인자 파싱

호출 시 받은 인자를 검사한다.

- 인자 없음 → 다음 명령으로 latest tag 조회:
  ```bash
  git fetch --tags origin
  git tag --sort=-v:refname | head -5
  ```
  결과를 사용자에게 보여주고 AskUserQuestion 으로 "다음 버전을 입력하세요 (예: `v0.1.0`)" 단답형 입력 받기. 첫 릴리즈라 태그가 없으면 `(none — 첫 릴리즈)` 라고 안내.
- 인자 있음 → 그대로 사용. `v` prefix 가 없으면 자동으로 붙인다 (`0.1.0` → `v0.1.0`).
- semver 형식 검증: `^v\d+\.\d+\.\d+(-[A-Za-z0-9.-]+)?$`. 어긋나면 중단 + 사유.
- 이미 존재하는 tag 인지 확인:
  ```bash
  git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null 2>&1 \
    || git ls-remote --tags origin "$VERSION" | grep -q .
  ```
  하나라도 hit 면 중단 ("이미 존재하는 태그입니다 — 새 버전을 입력하세요").

`VERSION` 변수에 `v0.1.0` 형태로 저장해 다음 단계로 넘긴다.

## 2. 사전 검증

각 항목 어긋나면 중단 + 사유 출력. 우회 옵션은 두지 않는다.

### 2.1 default 브랜치 + 동기화

```bash
BASE=$(gh repo view --json defaultBranchRef -q .defaultBranchRef.name)
HEAD=$(git rev-parse --abbrev-ref HEAD)
```

- `HEAD != BASE` → 중단. "릴리즈는 default 브랜치(`$BASE`)에서만 — `git checkout $BASE` 후 다시 시도하세요." 안내.
- `git fetch origin "$BASE"` 실행.
- `git rev-list --count "HEAD..origin/$BASE"` 가 0 이 아니면 (= 로컬이 원격보다 뒤짐) 중단 + `git pull` 안내.

### 2.2 clean working tree

```bash
git status --porcelain
```

비어있지 않으면 중단. release 는 commit 된 상태에서만. 사용자가 의도적으로 미커밋 변경분을 들고 있을 수 있어 자동 stash/commit 하지 않는다.

### 2.3 CI green

`develop` 의 가장 최근 ci.yml run 이 success 인지 확인.

```bash
gh run list -b "$BASE" -w ci.yml -L 1 --json conclusion,status,headSha
```

- `status` 가 `completed` 가 아니면 (예: `in_progress`) → 중단 + "CI 가 끝나길 기다린 뒤 다시 시도하세요." 안내.
- `conclusion` 이 `success` 가 아니면 → 중단 + 사유. CI red 인 채로 release 만들지 않는다.
- `headSha` 가 현재 HEAD 와 다르면 (= 로컬이 더 앞섰는데 그 commit 의 CI 가 아직 안 돌았음) → 중단 + "현재 HEAD 의 CI 가 아직 안 돌았습니다 — push 하셨다면 잠깐 기다려주세요."

### 2.4 gh CLI / goreleaser 가용성

```bash
gh auth status
command -v goreleaser
```

- `gh auth status` 실패 → 중단 + `gh auth login` 안내.
- goreleaser 없으면 → 중단 + `brew install goreleaser/tap/goreleaser` 안내. dry-run 단계에서 필수.

## 3. Dry-run (goreleaser snapshot)

태그를 만들기 전에 빌드가 실제로 통과하는지, 자산 이름 패턴이 `gh extension install` 과 호환되는지 검증한다. release 를 발행하지 않으므로 안전하다.

```bash
goreleaser release --snapshot --clean --skip=publish
```

성공하면 산출물을 검사한다.

### 3.1 산출물 존재 확인

```bash
ls dist/ | grep '^gh-orbit_'
```

기대 매트릭스 (`.goreleaser.yml` 의 `builds.goos × goarch` 에서 `windows/arm64` 제외):

- `darwin-amd64`, `darwin-arm64`
- `linux-amd64`, `linux-arm64`
- `windows-amd64` (`.exe` 확장자)

5 개 모두 dist/ 에 있어야 한다. 빠진 게 있으면 중단 + 사용자에게 `.goreleaser.yml` 의 `builds` 매트릭스 점검 요청.

### 3.2 gh extension lookup 호환성

cli/cli 의 extension manager (`pkg/cmd/extension/manager.go`, `installBin`) 가 자산을 찾는 규칙은 다음 한 줄이다:

```go
strings.HasSuffix(a.Name, platform+ext)
// platform = runtime.GOOS + "-" + runtime.GOARCH  (e.g., "darwin-arm64")
// ext      = ".exe" on windows, "" otherwise
```

즉 자산 이름의 **prefix 는 자유**고 **`<os>-<arch>[.exe]` suffix 만 매치**되면 인식된다. 현재 `.goreleaser.yml` 의 `gh-orbit_0.1.0_darwin-arm64` 는 `darwin-arm64` 로 끝나므로 호환된다 — underscore/dash 혼용이라도 문제없다.

검증:

- dist/ 의 각 산출물 파일명이 다음 5개 suffix 중 하나로 끝나는지:
  - `darwin-amd64`, `darwin-arm64`, `linux-amd64`, `linux-arm64`, `windows-amd64.exe`
- 모두 통과하면 §4 로 진행.
- 어긋나는 게 있으면 중단 + 사유 출력. `.goreleaser.yml` 의 `archives.name_template` 검토 안내 (suffix 가 깨지는 경우는 보통 `name_template` 을 직접 손댄 직후뿐).

### 3.3 GoReleaser warning

`goreleaser release --snapshot --clean --skip=publish` 출력에서 `WARN`, `error` 라인이 있으면 사용자에게 노출. deprecation warning 은 알리고 진행해도 되지만 `error` 는 중단.

## 4. 사용자 확인

dry-run 결과를 한 화면에 요약:

- 발행할 버전 (`$VERSION`)
- 산출물 목록 (5개 binary 이름) + 각 이름의 platform suffix 매치 여부 (5/5 OK)
- GoReleaser 경고 유무
- 다음 단계 — `git tag -a $VERSION -m "Release $VERSION"` + `git push origin $VERSION` → release.yml 자동 동작

AskUserQuestion 으로 "이대로 release 를 진행할까요?" 묻는다. 거부하면 중단 (로컬 변경 없으므로 cleanup 불필요).

## 5. 태그 생성 + push

```bash
git tag -a "$VERSION" -m "Release $VERSION"
git push origin "$VERSION"
```

- `git tag -a` 실패 (예: 동시에 다른 곳에서 같은 태그 생성됨) → 중단 + 사유.
- `git push` 실패 → 로컬 태그 정리 안내:
  ```bash
  git tag -d "$VERSION"
  ```
  사용자에게 push 실패 사유 (네트워크/권한 등) 확인 요청.

## 6. Actions 모니터링

태그 push 가 release.yml 을 트리거한다. webhook 도착까지 약간의 지연이 있다.

```bash
sleep 5
RUN_ID=$(gh run list -w release.yml -L 5 --json databaseId,headBranch,event,createdAt \
  -q "[.[] | select(.event == \"push\")][0].databaseId")
```

`RUN_ID` 가 비면 추가로 10초 대기 후 재시도. 그래도 비면 사용자에게 GitHub Actions 페이지 직접 확인 요청 + 중단 (태그는 이미 push 됐으므로 release 는 별도로 만들어지거나 안 만들어진다).

`RUN_ID` 확보 후:

```bash
gh run watch "$RUN_ID" --exit-status
```

- exit 0 → §7 으로.
- exit non-zero → 실패 로그:
  ```bash
  gh run view "$RUN_ID" --log-failed | tail -100
  ```
  사용자에게 출력 + "release 자산이 부분/누락 상태일 수 있습니다. `gh release view $VERSION` 으로 확인 후 필요하면 `gh release delete $VERSION --cleanup-tag` 로 정리하고 워크플로우를 고쳐 다시 시도하세요." 안내. 정리는 destructive 라 사용자가 직접 한다.

## 7. 사후 자산 검증

워크플로우가 success 로 끝나도 자산이 실제로 어떤 이름으로 올라갔는지, `gh extension install` 이 인식하는지 별도로 확인한다 — §3.2 에서 호환성을 의심한 케이스라면 여기가 진짜 답을 준다.

### 7.1 release 자산 목록

```bash
gh release view "$VERSION" --json assets -q '.assets[].name'
```

5개 binary 이름이 모두 떠야 한다. 하나라도 없으면 사용자에게 보고 + 워크플로우 로그 확인 권장.

### 7.2 실제 install 검증

`gh extension install` 이 동작하는지 임시 GH_CONFIG_DIR 로 검증한다. 사용자의 진짜 환경을 건드리지 않는다.

```bash
TMPDIR=$(mktemp -d)
GH_CONFIG_DIR="$TMPDIR/gh" gh extension install jeonbyeongmin/gh-orbit 2>&1
```

- 성공 → "✓ `gh extension install jeonbyeongmin/gh-orbit` 동작 확인" 보고.
- "no compatible asset" 류 실패 → 자산 이름 패턴 호환성 문제. 사용자에게:
  > "release 자산이 인식되지 않습니다. `.goreleaser.yml` 의 `name_template` 을 underscore-only 로 바꾸고 다음 패치 버전을 발행하세요 (이번 release 는 그대로 둬도 무방 — 다음 버전이 나오면 자동으로 그쪽이 latest 가 됩니다)."

임시 디렉토리 정리:

```bash
rm -rf "$TMPDIR"
```

## 8. 보고

성공 시:

```
✓ release published: $VERSION
  url: https://github.com/jeonbyeongmin/gh-orbit/releases/tag/$VERSION
  assets: 5/5 (darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64)
  install check: OK

다음 명령으로 직접 확인할 수 있습니다:
  gh extension remove orbit 2>/dev/null
  gh extension install jeonbyeongmin/gh-orbit
  gh orbit
```

실패/부분 성공 시:

- 어느 단계에서 멈췄는지 (예: §6 워크플로우 fail)
- 현재 GitHub 측 상태 (태그/release 가 남아있는지)
- 사용자가 다음에 할 일 (release 정리 명령, .goreleaser.yml 수정 포인트 등)

이 스킬은 멀쩡한 release 한 개를 발행하는 것이 목표다 — 부분 실패가 발생하면 절대 자동으로 cleanup 하지 않고 사용자에게 위임한다. 잘못된 cleanup 은 손으로 만든 history 를 날릴 수 있다.
