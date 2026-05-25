---
name: pr
description: gh-orbit 전용 /pr 스킬. user-level /pr 이 §1 에서 자동 위임하는 본문이며, Go 1.26 + Bubble Tea 프로젝트의 검증 4단계(gofmt → golangci-lint/go vet 폴백 → go build → go test) 를 통과한 PR 만 만든다. base 는 default 브랜치(develop) 고정, --force/--no-verify 금지, 검증 실패는 우회하지 않고 사용자에게 위임. project-local 컨벤션이 user-level 의 Node 자동 감지를 대체한다.
---

# /pr (gh-orbit project-local)

이 파일은 user-level `/pr` (`~/.claude/skills/pr/SKILL.md`) 이 §1 에서 자동 위임하는 project-local 본문이다. user-level 은 lockfile / `package.json scripts` 로 검증 명령을 자동 감지하는데, gh-orbit 은 Go 프로젝트라 그 자동 감지가 동작하지 않는다 — 그래서 검증 단계만 Go 컨벤션으로 갈아낀다.

위임 흐름:

1. 부모(`/execute-plan`, 사용자 직접 호출 등)가 `Skill(skill="pr")` 호출
2. user-level §1 이 `<repo>/.claude/skills/pr/SKILL.md` 존재 확인 → 이 파일을 Read
3. 한 줄 안내 후 이 본문의 절차를 끝까지 그대로 수행
4. user-level §2 이하는 더 따르지 않음
5. 이 스킬의 §8 보고가 부모 호출의 결과

원칙은 user-level 과 동일하다 — `--force` / `--no-verify` 자동 사용 금지, dirty tree 는 사용자에게 1회 확인, 검증 실패는 우회 대상이 아니라 수정 대상. 다른 점은 검증 명령이 Go 표준이라는 것뿐.

## 0. AskUserQuestion 로드

dirty 워킹 트리 처리에서 사용자에게 한 번 물어야 한다. 시작 직전에:

```
ToolSearch(query="select:AskUserQuestion", max_results=1)
```

## 1. Pre-flight 검증

각 항목 어긋나면 중단 + 사용자에게 보고. `--force`, `--no-verify` 같은 강제 옵션은 절대 쓰지 않는다.

- **현재 브랜치**: `git rev-parse --abbrev-ref HEAD`. `develop` / `main` / `master` 면 중단 (작업 브랜치 만들고 다시 시도하라고 안내).
- **base 브랜치 결정**:
  ```bash
  BASE=$(gh repo view --json defaultBranchRef -q .defaultBranchRef.name)
  ```
  현재 default 는 `develop`. fallback 으로 `git symbolic-ref refs/remotes/origin/HEAD` 의 끝부분.
- **원격 동기화**: `git fetch origin "$BASE"`.
- **HEAD 가 base 보다 앞**: `git rev-list --count "origin/$BASE..HEAD"` 가 1 이상.
- **gh CLI 인증**: `gh auth status`. 실패 시 `gh auth login` 안내 후 중단.
- **워킹 트리**:
  - 깨끗하면 그대로 통과.
  - dirty 면 `git status -s` + `git diff` 보여주고 AskUserQuestion 으로 "현재 변경분도 PR 에 포함할까요?" 한 번 묻는다.
    - 동의 → 변경 성격을 보고 Angular type 을 추론해 한 줄 한국어 메시지로 커밋. scope 모호하면 생략.
    - 거부 → 중단.

## 2. /code-review 호출 (항상 실행)

검증 4단계 전에 **항상** `/code-review` 를 실행한다. `/code-review` 는 자체 SKILL 본문을 로드하고 base..HEAD diff 를 재분석한 뒤, 발견한 문제가 있으면 추가 커밋으로 반영한다.

```
Skill(skill="code-review")
```

파일이 실제로 수정됐다면:

```bash
git add -u
git commit -m "refactor: /code-review 결과 반영"
```

scope 가 명확하면 `refactor(<scope>): /code-review 결과 반영`. 변경 없으면 커밋 없이 다음 단계로.

## 3. 검증 4단계

순서: format → lint → build → test. 한 단계라도 실패하면 그 시점에 멈추고 출력을 그대로 사용자에게 보여준다. 스킬이 임의로 룰을 비활성화하거나 `//nolint` 를 코드에 삽입하거나 `go test -short` 로 우회하지 않는다.

### 3.1 Format — gofmt

```bash
DIRTY=$(gofmt -l .)
```

- `DIRTY` 가 비어있으면 통과.
- 비어있지 않으면:
  ```bash
  gofmt -w .
  git add -u
  git commit -m "style: gofmt 자동 수정"
  ```
  사용자가 "정리 커밋 없이" 를 미리 요청했다면 자동 수정·커밋을 건너뛰고 대신 `gofmt -l .` 결과를 보여주고 중단한다 (사용자가 수동 처리).

검증 통과로 `✓ gofmt` 보고.

### 3.2 Lint — golangci-lint 우선, go vet 폴백

CLAUDE.md 의 baseline 은 `golangci-lint run` 이지만 PATH 설치는 보장되지 않는다. 따라서:

```bash
if command -v golangci-lint >/dev/null 2>&1; then
  golangci-lint run
  LINT_TOOL="golangci-lint run"
else
  echo "golangci-lint 미설치 — go vet 으로 폴백"
  go vet ./...
  LINT_TOOL="go vet ./..."
fi
```

실패 시 출력 그대로 보고하고 중단. 통과 시 `✓ $LINT_TOOL` 보고. **`LINT_TOOL` 값은 §6 PR 본문 자동 검증 체크박스에 그대로 사용한다 — 실제로 실행한 도구만 표기하기 위함.**

> 폴백 정책의 이유: `.golangci.yml` 이 없는 현재 상태에서 golangci-lint 미설치 사용자에게 PR 생성을 막는 건 마찰만 키운다. `go vet` 도 충분히 의미 있는 정적 검사이므로 폴백으로 둔다. 이 정책은 `.golangci.yml` 이 추가되면 재검토 — 룰셋이 명문화된 시점부터는 강제할 가치가 생긴다.

### 3.3 Build

```bash
BUILD_OUT=$(mktemp -t gh-orbit-build.XXXXXX)
trap 'rm -f "$BUILD_OUT"' EXIT
go build -o "$BUILD_OUT" ./cmd/orbit
```

- 산출물은 `/tmp` 에 두고 종료 시 정리한다. workspace 에 `gh-orbit` 바이너리를 만들지 않는 이유: gitignored 라 git 은 안 어지럽혀도, 다음 호출에서 `gofmt -l .` 등이 바이너리를 만나는 일을 피하고, 빌드 산출물이 동시 실행 사이에 충돌하지 않게 한다.
- 컴파일 실패는 자동 수정 시도하지 않는다 — 출력 그대로 보고하고 중단.

통과 시 `✓ go build ./cmd/orbit` 보고.

### 3.4 Test

```bash
go test ./...
```

실패 시 출력 그대로 보고하고 중단. 통과 시 `✓ go test ./...` 보고.

## 4. Push

```bash
BR=$(git rev-parse --abbrev-ref HEAD)
if git rev-parse --abbrev-ref --symbolic-full-name @{u} >/dev/null 2>&1; then
  git push
else
  git push -u origin "$BR"
fi
```

비-ff 거부 (원격에 다른 커밋이 있으면) 시 중단 + 보고. `--force` / `--force-with-lease` 자동 사용 금지. 사용자에게 rebase/merge 결정 위임.

## 5. 기존 PR 확인

```bash
gh pr view --json number,url,state 2>/dev/null
```

- OPEN 인 PR 이 이미 있으면 새로 만들지 않는다 — push 만으로 갱신됨. URL 만 보고하고 종료.
- 없으면 다음 단계.

## 6. PR 메타데이터 작성

### Title

`git log "origin/$BASE..HEAD" --pretty=format:"%s"` 로 본 커밋 제목들에서 한국어 Angular 컨벤션 한 줄 제목을 만든다.

- 의미 있는 커밋이 1 개면 그 메시지를 그대로 (chore/style 만 모인 경우는 제외하고 가장 의미 있는 type 우선).
- 여러 개면 type 다수결 + 핵심 종합. 50자 이내, 마침표 없음.
- `$ARGUMENTS` 가 있으면 제목 hint 로 활용.

scope 후보 (gh-orbit 에 맞춤): `tui`, `git`, `gh`, `config`, `cmd`, `build`, `docs`. 모호하면 scope 생략.

예시:
- `feat(tui): 3-pane Fork 레이아웃 구현`
- `fix(git): merge commit 다중 부모 파싱 처리`
- `refactor(config): XDG 경로 해석 단순화`

### Body — 한국어 템플릿

```markdown
## 요약
- <변경 핵심 1~3 bullet>

## 자동 검증
- [x] gofmt
- [x] <golangci-lint run | go vet ./...>
- [x] go build ./cmd/orbit
- [x] go test ./...

## 수동 검증
- [ ] `go run ./cmd/orbit` 로 TUI 정상 기동 확인
- [ ] `hjkl` 이동·`:` 명령어 입력 등 기본 키바인딩 동작 확인
- [ ] `~/.local/state/gh-orbit/log` 에 비정상 에러 미기록

## 관련
- (이슈 / 문서 링크가 있으면, 없으면 섹션 통째로 생략)
```

세부 규칙:

- **자동 검증 체크박스**는 §3 에서 *실제로 실행해 통과한* 명령만 `[x]` 로 표기. lint 줄은 §3.2 에서 결정한 `LINT_TOOL` 값을 그대로 적는다 — `golangci-lint run` 이거나 `go vet ./...` 둘 중 하나.
- **수동 검증 체크박스**는 항상 비어있는 `[ ]` 로 삽입한다. `go test ./...` 만으로는 TUI 화면 회귀를 잡을 수 없어 리뷰어/PR 작성자가 직접 화면을 띄워 확인해야 한다는 컨벤션을 강제하기 위함이다. 셀프 체크 후 GitHub 에서 체크박스를 켠다.
- 본문은 한국어. 섹션 헤더 (`## 요약`, `## 자동 검증`, `## 수동 검증`, `## 관련`) 는 그대로 유지.
- 코드 식별자(함수명, 옵션명, 경로) 는 영문/원문 그대로. 한국어로 번역하지 않는다.

## 7. PR 생성

```bash
gh pr create \
  --base "$BASE" \
  --head "$BR" \
  --title "$TITLE" \
  --body "$(cat <<'EOF'
$BODY
EOF
)"
```

HEREDOC 으로 본문 전달해 따옴표·백틱 깨지지 않게 한다.

## 8. Plan 아카이브 (vault 갱신)

PR 생성/확인이 끝나면 vault 의 plan 한 개를 PR 에 연결하고 archive 로 옮긴다. user-level `/pr` §9 와 동일한 규약 — project-local 위임 흐름이 user-level 본문을 안 따르므로 여기 그대로 박는다.

### 8.1 vault / 프로젝트 / 브랜치 slug 결정

```bash
PROJECT=$(git config --get remote.origin.url 2>/dev/null \
  | sed -E 's|.*[:/]([^/]+?)(\.git)?$|\1|')
[ -z "$PROJECT" ] && PROJECT=$(basename "$(git rev-parse --show-toplevel 2>/dev/null)")

VAULT_ROOT="/Users/jeonbyeongmin/Library/Mobile Documents/iCloud~md~obsidian/Documents/project-manager"
PROJECT_DIR="$VAULT_ROOT/$PROJECT"
SLUG="${BR##*/}"   # feat/foo-bar → foo-bar
```

`$PROJECT_DIR` 가 없으면 "vault 추적 없음 — 아카이브 스킵" 한 줄 보고 후 §9 로 점프 (실패 아님).

### 8.2 plan 매칭

```bash
shopt -s nullglob
matches=( "$PROJECT_DIR/plans/"*-"$SLUG".md )
shopt -u nullglob
```

- **단일 매칭**: §8.3
- **0 매칭**: "PR 과 매칭되는 plan 없음 — 아카이브 스킵 (브랜치 slug: `$SLUG`)" 보고 후 §9
- **다수 매칭** (`-bundle` 등): "다수 plan 매칭: <목록>. 모호하여 스킵" 보고 후 §9

### 8.3 frontmatter 갱신 + archive 이동

1. **plan frontmatter 갱신** (Edit):
   ```yaml
   pr: <PR URL>
   status: shipped
   shipped_at: <YYYY-MM-DD>
   ```
2. **backlog frontmatter 갱신**: plan 의 `source_backlog` 가 가리키는 `archive/backlogs/<file>.md` 에:
   ```yaml
   linked_pr: <PR URL>
   ```
   `source_backlog` 누락/파일 부재면 backlog 갱신만 스킵 (보고에 한 줄).
3. **iCloud 충돌 사본 검사**: `<file> 2.md` 등이 `plans/` · `archive/plans/` 에 있으면 mv 중단 + plan frontmatter 에 `archive_pending: true` 추가 + §9 로 이동. 사용자가 충돌 정리 후 수동.
4. **archive 이동**:
   ```bash
   mv "$PROJECT_DIR/plans/<file>.md" "$PROJECT_DIR/archive/plans/<file>.md"
   ```

## 9. 사용자에게 보고

다음을 한 번에:

- 거친 검증 단계와 결과 (실제로 실행한 것만):
  - `✓ gofmt`
  - `✓ golangci-lint run` 또는 `✓ go vet ./...` (폴백이면 폴백임을 한 줄 명시)
  - `✓ go build ./cmd/orbit`
  - `✓ go test ./...`
- 스킬이 만든 추가 커밋 (있으면): `refactor: /code-review 결과 반영`, `style: gofmt 자동 수정` 등
- push 한 브랜치 + upstream
- 생성된 PR URL (또는 이미 OPEN 이던 PR URL)
- base 브랜치 명시 (현재 default 는 `develop` — main 으로 바뀐 적 있으면 그것)
- §8 plan 아카이브 결과 (이동된 plan 경로 / 매칭 실패 사유 / archive_pending 등)
- 마지막 줄: `(project-local /pr 규약 적용)`

## 절대 하지 않을 것

- `--force`, `--force-with-lease`, `--no-verify` 자동 사용
- 검증 통과를 위해 `//nolint`, `// golint:ignore` 등을 코드에 삽입
- 검증 통과를 위해 `.golangci.yml` 룰을 수정 (있을 때)
- `go test -short` / `-run` 으로 테스트 일부만 돌려 통과 처리
- 검증 실패 시 출력을 잘라내거나 요약하기 — 그대로 사용자에게 보여준다
- `/gh-orbit` 빌드 산출물을 commit (gitignored 지만 명시적으로 추가 금지)

## 주의사항

- **`/code-review` 는 항상 실행** (§2 참조). `gofmt` 자동 수정은 §3.1 의 default 동작으로 유지 — 형식 위반은 PR 머지 전에 정리되는 게 cleanup 비용보다 가치가 크기 때문. 사용자가 "정리 커밋 없이" 미리 요청했다면 §3.1 의 자동 수정·커밋도 건너뛰고 `gofmt -l .` 결과를 보여주고 중단 (검증 자체는 반드시 실행).
- 비-ff push 거부 / 검증 실패 / dirty tree 거부는 모두 사용자 결정 사항으로 위임. 자동 우회 금지.
- 이 스킬은 `git push` 와 `gh pr create` 를 실행한다 — 외부 영향 (원격 갱신, 리뷰어 알림). pre-flight 다 통과한 뒤에야 push 하므로 사전 동의는 "PR 올려줘" 발화로 충분. dirty tree 포함 여부만 §1 에서 명시적으로 확인.
- 첫 push 는 `-u origin <branch>` 로 upstream 등록. 작업 브랜치가 origin 에 없는 케이스 흔함.
- PR 본문에 본인이 만든 커밋 SHA 를 나열하지 않는다 — gh 가 commits 탭에 자동으로 보여준다.
- `/execute-plan` 부모가 이 스킬을 호출했을 때 검증 실패하면 부모가 plan 아카이브 이동을 막는다 (이 스킬은 그 사실을 의식할 필요 없음 — 종료 코드/보고만 정확하면 됨).
