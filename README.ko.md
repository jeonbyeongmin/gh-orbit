<div align="center">

# gh-orbit

로컬 git 을 터미널에서 리뷰하는 `gh` CLI 익스텐션 — 커밋 그래프와 diff 를 읽고, 각 worktree 의 PR 을 키보드 중심의 단일 대시보드에서 리뷰하고 머지한다.

[![release](https://img.shields.io/github/v/release/jeonbyeongmin/gh-orbit?color=7c6f9f&label=release)](https://github.com/jeonbyeongmin/gh-orbit/releases)
&nbsp;[![gh extension](https://img.shields.io/badge/gh-extension-24292f?logo=github)](https://github.com/jeonbyeongmin/gh-orbit)
&nbsp;![platform](https://img.shields.io/badge/macOS%20·%20Linux%20·%20Windows-555)

[English](./README.md) · 한국어

<img src="./docs/assets/demo.gif" alt="gh-orbit 데모" width="860">

</div>

---

gh-orbit 은 저장소의 커밋 그래프, 커밋·워킹 트리 diff, 브랜치, worktree 를 하나의 터미널 UI 에서 보여준다. worktree 대시보드는 각 브랜치의 열린 PR 과 CI 상태를 한눈에 드러내므로, 작업이 여러 worktree 에 흩어져 있을 때 — 여러 코딩 에이전트든, 본인의 병렬 브랜치든 — 터미널을 떠나지 않고 각각의 PR 을 리뷰하고 머지할 수 있다. 모든 git 동작은 사용자의 `git` 바이너리로 shell-out 되므로 `.gitconfig`, 훅, 커밋 서명, LFS 가 그대로 동작하고, PR 데이터는 `gh` CLI 에서 가져온다.

## 설치

```bash
gh extension install jeonbyeongmin/gh-orbit
gh orbit
```

git 저장소 안에서 `gh orbit` 을 실행한다.

## 내부에서 실행되는 git / gh

각 동작은 사용자의 `git` / `gh` 를 실행한다 — gh-orbit 은 인터페이스이지 재구현이 아니다.

| 동작 | 실행 |
| --- | --- |
| 커밋 그래프 (실행 시) | `git log --all --graph --oneline --decorate` |
| `→` patch 오버레이 + `[` / `]` | `git show -p <commit>` 을 파일 단위로 |
| Local Changes (`tab` cycle) + `space` | `git status` + `git diff` + `git add` / `git restore --staged` |
| `[` / `]` + `space` (diff 패널) | hunk 단위 `git apply --cached` (`--reverse` 로 unstage) |
| `space` (체크아웃 / fast-forward) | `git checkout <branch>` / `git merge --ff-only <ref>` |
| `tab` → Worktrees (전환 / 추가 / 제거) | `git worktree list` / `add` / `remove` |
| `b` → `d` (브랜치 삭제) | `git branch -d <branch>` |
| `c` / `R` | `git cherry-pick <commit>` / `git rebase <onto>` |
| `v` / `x` | `git revert <commit>` / `git reset --soft\|--mixed\|--hard <commit>` |
| `n` | `git checkout -b <name> <commit>` |
| `F` / `p` / `P` | `git fetch --all` / `git pull` / `git push` |
| `enter` (PR 웹에서 열기) | `gh pr view <n> --web` |
| `m` (PR 머지) | `gh pr merge --squash\|--merge\|--rebase` |
| Pull Requests 탭 | `gh pr list` (재사용) → `enter` 로 행을 웹에서 열고, `m` 으로 머지 |
| 브랜치 칩의 PR 배지 | `gh pr list` + `gh pr checks <number>` |
| `y` | `git rev-parse <commit>` → 클립보드 |

## 기능

### 커밋 그래프

<img src="./docs/assets/commit-graph.gif" alt="commit graph" width="800">

- 실행 시 모든 로컬 브랜치·리모트·태그를 하나로 묶은 통합 그래프 (`--all`).
- 점 어휘: `●` 커밋 · `○` 머지 · `◉` HEAD. 레인 색상은 8색 팔레트를 순환한다.
- 각 행은 `그래프 │ 메시지(칩 + 제목) │ 작성자 │ 작성 시각` 으로 읽힌다. 시각은 오른쪽 끝에 고정되어 항상 보이고, 메시지 열이 truncation 을 흡수하며 제목이 1 cell 밑으로 줄기 전에 칩·작성자가 그 순서로 빠진다.
- 브랜치/태그 decoration 은 칩으로 렌더된다. 매칭되는 열린 PR 에는 `#N` 배지와 CI 롤업 글리프(`✓` 통과 · `✗` 실패 · `○` 진행 중)가 붙고, 실행 시·`r`·매 fetch/pull 후 갱신된다. GitHub 리모트가 없으면 배지를 생략한다.
- reload 는 stale-while-revalidate: 새 그래프가 스트리밍되는 동안 현재 그래프가 화면에 남는다.

### diff 리뷰

<img src="./docs/assets/diff-review.gif" alt="diff review" width="800">

- `→` 는 focus 커밋의 전체 patch(`git show -p`)를 전체 화면 오버레이로 연다.
- `[` / `]` 는 패치 안에서 파일을 오가고, 하단에 `<path> [N/M]` 이 표시된다.
- **Local Changes** 페이지(`tab` / `shift+tab` 순환에 포함)는 워킹 트리 diff(파일 트리 + diff 패널)를 Conflicts / Unstaged / Staged 로 나눈다. `→` 로 diff 패널에 들어가고, `←` 로 트리로 돌아가며, `space` 로 focus 파일 stage/unstage, `r` 로 reload.
- hunk 단위 staging: `tab` 으로 diff 패널에 들어가 `[` / `]` 로 hunk 사이를 이동하고(선택된 `@@` 헤더가 강조됨), `space` 로 그 hunk 만 stage(`git apply --cached`) — staged 항목을 보고 있으면 unstage. untracked / conflict 파일은 트리에서 파일 전체로 stage.

### Pull requests (`enter` / `m` / Pull Requests 탭)

<img src="./docs/assets/pull-requests.gif" alt="pull requests" width="800">

리뷰는 GitHub 에서 일어난다; 코크핏은 거기로 점프시키고 PR 을 머지한다.

- 칩에 열린 PR 배지가 달린 커밋에서 `enter` 는 그 PR 을 브라우저의 GitHub 에서 연다(`gh pr view --web`). 같은 웹 점프가 Worktree 와 Pull Requests 페이지의 `enter` 에도 연결되어 있다.
- `m` 은 커서 행의 열린 PR 을 중앙 confirm 다이얼로그로 머지한다 — `[s]` squash · `[m]` merge · `[r]` rebase(`gh pr merge`). 머지하면 다이얼로그가 닫히며 PR 배지를 갱신하고, gh 에러(머지 불가, 로그아웃된 `gh`)는 상태 줄에 표시된다.
- **Pull Requests 탭**(`tab` / `shift+tab` 순환의 마지막)은 모든 열린 PR 을 나열한다 — 로컬에 체크아웃되지 않아 그래프 커서가 닿지 못하는 head 브랜치의 PR 까지 포함한다. 행은 `#N <CI 글리프> 제목 · 작성자` 로 읽히고, `enter` 는 커서 행을 웹에서 열고, `m` 은 머지하며, `r` 은 목록을 갱신한다.

### worktree (`tab`)

<img src="./docs/assets/worktrees.gif" alt="worktrees dashboard" width="800">

- `tab` 은 전체화면 대시보드로 순환 이동한다 — worktree 당 3줄 카드(브랜치 + PR/CI + dirty/시각 · 경로 · 마지막 커밋) 로 모든 트리 상태를 한눈에 본다. `tab` / `shift+tab` 으로 다음 / 이전 페이지로 이동한다.
- `space` 는 UI 전체를 다른 worktree 로 프로세스 내부에서 전환한다. `enter` 는 그 worktree 의 열린 PR 을 브라우저의 GitHub 에서 연다 — 각 에이전트의 PR 을 웹에서 리뷰하고, 그래프 / Pull Requests 탭에서 `m` 으로 머지한다.
- `a` 로 worktree 추가(형제 경로 자동 도출), `d` 로 제거(dirty/locked 는 force 확인), `s` 로 마지막 커밋 시각순 정렬.
- 각 카드에 열린 PR `#N` + CI 배지(있을 때), upstream 대비 `↑a↓b`(ahead/behind), `●N` dirty 마커(`N` = 변경 파일 수), worktree HEAD 의 마지막 커밋 제목·상대 시각이 표시된다.
- `.git/HEAD` 와 `.git/index` 를 fsnotify 로 감시해 외부 커밋·rebase 가 목록을 갱신하고, `r` 은 항상 수동 폴백이다.

### 브랜치 & 체크아웃

<img src="./docs/assets/branches.gif" alt="branches and checkout" width="800">

- 그래프에서 `space` 는 커서의 칩 상태와 HEAD 관계로 체크아웃 / fast-forward / detach 를 고른다. 모호한 행은 브랜치 피커를 연다.
- 체크아웃이 깨끗한 트리를 요구하면 `s` 로 stash 후 계속, `a` / `esc` 로 중단한다.
- `n` 은 커서에 브랜치를 만들고 전환한다.
- `b` 는 로컬 브랜치를 나열하고 `d` 로 커서 브랜치를 삭제한다(HEAD 보호).

### 히스토리 조작

- `c` 는 커서 커밋을 현재 브랜치에 cherry-pick, `R` 은 현재 브랜치를 커서 커밋 위로 rebase 한다.
- `v` 는 커서 커밋을 revert 한다(새 커밋 기록; 이미 push 된 히스토리에도 안전).
- `x` 는 현재 브랜치를 커서 커밋으로 reset 한다 — `[s]` soft / `[m]` mixed / `[h]` hard. push 된 히스토리를 다시 쓰는 reset 은 거부하고 `v` 로 안내한다.
- 네 가지 모두 확인 우선이며, 충돌은 워킹 트리에 남겨 터미널에서 해결한다.

### 네트워크 & 그 외 키

- `F` fetch(`git fetch --all`) · `p` pull(strategy 해석) · `P` push(첫 push 는 upstream 설정; 절대 force 안 함).
- `y` 는 커밋 해시 복사 · `r` 은 refs + log reload.
- `?` 는 현재 페이지 기준의 인라인 help 패널을 토글한다(Global + 그 페이지 키 컬럼).

## 키 바인딩

| 키 | 위치 | 동작 |
| --- | --- | --- |
| `↑` / `↓` · `g` / `G` | 그래프 | 이동 · 맨 위 / 맨 아래로 |
| `space` | 그래프 | 체크아웃 / fast-forward / detach |
| `→` | 그래프 | 전체 화면 patch 오버레이 열기 |
| `enter` | 그래프 | 커서 행의 열린 PR 을 웹에서 열기 |
| `m` | 그래프 | 커서 행의 열린 PR 머지 (`s`/`m`/`r` 전략) |
| `[` / `]` | 패치 | 이전 / 다음 파일로 점프 |
| `enter` / `m` | pull requests | 웹에서 열기 / 커서 PR 머지 |
| `tab` / `⇧tab` | 전역 | 페이지 순환 (그래프 · worktree · local changes · pull requests) |
| `space` | local changes | focus 파일 stage / unstage (diff 패널에선 hunk) |
| `[` / `]` | local changes diff | 이전 / 다음 hunk |
| `b` | 전역 | 브랜치 모달 |
| `c` / `R` / `v` / `x` | 그래프 | cherry-pick / rebase / revert / reset |
| `n` | 그래프 | 커서에 브랜치 생성 + 전환 |
| `F` / `p` / `P` | 전역 | fetch / pull / push |
| `y` | 그래프 | 해시 복사 |
| `r` | 전역 | reload |
| `?` | 전역 | help 패널 토글 |
| `ctrl+c` `ctrl+c` | 전역 | 종료(두 번 누르기) |

## 설정

XDG 규격 경로(`internal/config` 가 해석 담당):

- 환경설정 — `$XDG_CONFIG_HOME/gh-orbit/config.toml`(선택):

  ```toml
  [pull]
  strategy = "rebase"   # "ff-only" | "merge" | "rebase"
  ```

  pull strategy 해석 순서: 환경설정 `[pull] strategy` → git config `pull.rebase` → 폴백 `--ff-only`.
- 로그 — `$XDG_STATE_HOME/gh-orbit/log`(TUI 가 stdout 을 소유하므로 런타임 로깅이 여기로 간다).

## 개발

```bash
go run ./cmd/orbit                    # cwd 저장소 대상으로 실행
go test ./...                         # 테스트
golangci-lint run                     # 린트
tail -f ~/.local/state/gh-orbit/log   # 런타임 로그 추적 (TUI 가 stdout 소유)
```

데모 GIF 는 [VHS](https://github.com/charmbracelet/vhs) 로 재생성한다:

```bash
go build -o /tmp/orbit-demo ./cmd/orbit
vhs docs/assets/demo.tape
```

기능 단위 레퍼런스는 [`docs/`](./docs/) 참고 — [`docs/index.md`](./docs/index.md) 에서 시작.
