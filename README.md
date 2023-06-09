# shutdown

Shutdown management library for Go

This package helps you manage shutdown code centrally, and provides functionality to execute code when a controlled shutdown occurs.

This will enable you to save data, notify other services that your application is shutting down.

* Package home: <https://github.com/eikmadsen/shutdown>
* Godoc: <https://godoc.org/github.com/eikmadsen/shutdown>

[![GoDoc][1]][2] [![Build Status][3]][4]

[1]: https://godoc.org/github.com/eikmadsen/shutdown?status.svg
[2]: https://godoc.org/github.com/eikmadsen/shutdown
[3]: https://travis-ci.org/eikmadsen/shutdown.svg
[4]: https://travis-ci.org/eikmadsen/shutdown

## concept

Managing shutdowns can be very tricky, often leading to races, crashes and strange behavior.
This package will help you manage the shutdown process and will attempt to fix some of the common problems when dealing with shutting down.

The shutdown package allow you to block shutdown while certain parts of your code is running.
This is helpful to ensure that operations are not interrupted.

The second part of the shutdown process is notifying goroutines in a select loop and calling functions in
your code that handles various shutdown procedures, like closing databases,
notifying other servers, deleting temporary files, etc.

The second part of the process has three **stages**, which will enable you to do your shutdown in stages.
This will enable you to rely on some parts, like logging, to work in the first two stages.
There is no rules for what you should put in which stage, but things executing in stage one can safely rely on stage two not being executed yet.

All operations have **timeouts**.
This is to fix another big issue with shutdowns; applications that hang on shutdown.
The timeout is for each stage of the shutdown process, and can be adjusted to your application needs.
If a timeout is exceeded the next shutdown stage will be initiated regardless.

Finally, you can always cancel a notifier, which will remove it from the shutdown queue.

## usage

First get the libary with `go get -u github.com/eikmadsen/shutdown`,
and add it as an import to your code with `import github.com/eikmadsen/shutdown`.

The next thing you probably want to do is to register Ctrl+c and system terminate.
This will make all shutdown handlers run when any of these are sent to your program:

```Go
 s := shutDown.New()
 s.OnSignal(0, os.Interrupt, syscall.SIGTERM)
```

If you don't like the default timeout duration of 5 seconds, you can change it by calling the `SetTimeout` function:

```Go
  s.SetTimeout(time.Second * 1)
```

Now the maximum delay for shutdown is **4 seconds**.
The timeout is applied to each of the stages and that is also the maximum time to wait for the shutdown to begin.
If you need to adjust a single stage, use `SetTimeoutN` function.

Next you can register functions to run when shutdown runs:

```Go
  logFile := os.Create("log.txt")

  // Execute the function in the first stage of the shutdown process
  _ = s.FirstFn(func(){
    logFile.Close()
  })

  // Execute this function in the second part of the shutdown process
  _ = s.SecondFn(func(){
    _ = os.Delete("log.txt")
  })
```

As noted there are three stages.
All functions in one stage are executed in parallel.
The package will wait for all functions in one stage to have finished before moving on to the next one.  
So your code cannot rely on any particular order of execution inside a single stage,
but you are guaranteed that the First stage is finished before any functions from stage two are executed.

This example above uses functions that are called, but you can also request channels that are notified on shutdown.
This allows you do have shutdown handling in blocked select statements like this:

```Go
  go func() {
    // Get a stage 1 notification
    finish := s.First()
    select {
      case n:= <-finish:
        log.Println("Closing")
        close(n)
        return
  }
```

If you suspect that shutdown may already be running, you should check the returned notifier.
If shutdown has already been initiated, and has reached or surpassed the stage you are requesting a notifier
for, `nil` will be returned.

```Go
    // Get a stage 1 notification
    finish := s.First()
    // If shutdown is at Stage 1 or later, nil will be returned 
    if finish == nil {
        log.Println("Already shutting down")
        return
    }
    select {
      case n:= <-finish:
        log.Println("Closing")
        close(n)
        return
  }
```

If you for some reason don't need a notifier anymore you can cancel it.
When a notifier has been cancelled it will no longer receive notifications,
and the shutdown code will no longer wait for it on exit.

```Go
  go func() {
    // Get a stage 1 notification
    finish := shutdown.m.First()
    select {
      case n:= <-finish:
        close(n)
        return
      case <-otherchan:
        finish.Cancel()
        return
  }
```

Functions are cancelled the same way by cancelling the returned notifier.
Be aware that if shutdown has been initiated you can no longer cancel notifiers, so you may need to aquire a shutdown lock (see below).

If you want to Cancel a notifier, but shutdown may have started, you can use the CancelWait function.
It will cancel a Notifier, or wait for it to become active if shutdown has been started.

If you get back a nil notifier because shutdown has already reached that stage, calling CancelWait will return at once.

```Go
  go func() {
    // Get a stage 1 notification
    finish := s.First()    
    if finish == nil {
        return 
    }
    select {
      case n:= <-finish:
        close(n)
        return
      case <-otherchan:
        // Cancel the finish notifier, or wait until Stage 1 is complete.
        finish.CancelWait() 
        return
  }
```

The final thing you can do is to lock shutdown in parts of your code you do not want to be interrupted by a shutdown,
or if the code relies on resources that are destroyed as part of the shutdown process.

A simple example can be seen in this http handler:

```Go
 http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
  // Acquire a lock.
  lock := s.Lock()
  // While this is held server will not shut down (except after timeout)
  if lock == nil {
   // Shutdown has started, return that the service is unavailable
   w.WriteHeader(http.StatusServiceUnavailable)
   return
  }
  // Defer unlocking the lock.
  defer lock()
  io.WriteString(w, "Server running")
 })
```

If shutdown is started, either by a signal or by another goroutine, it will wait until the lock is released.
It is important always to release the lock, if s.Lock() returns a notifier returning Valid()==true.
Otherwise the server will have to wait until the timeout has passed before it starts shutting down, which may not be what you want.

As a convenience we also supply wrappers for [`http.Handler`](https://godoc.org/github.com/eikmadsen/shutdown#WrapHandler)
and [`http.HandlerFunc`](https://godoc.org/github.com/eikmadsen/shutdown#WrapHandlerFunc), which will do the same
for you.

Each lock keeps track of its own creation time and will warn you if any lock exceeds
the deadline time set for the pre-shutdown stage.
This will help you identify issues that may be with your code,
where it takes longer to complete than the allowed time, or you have forgotten to unlock any aquired lock.

Finally you can call `s.Exit(exitcode)` to call all exit handlers and exit your application.
This will wait for all locks to be released and notify all shutdown handlers and exit with the given exit code.
If you want to do the exit yourself you can call the `shutdown.m.Shutdown()`, which does the same, but doesn't exit.
Beware that you don't hold a lock when you call Exit/Shutdown.

Do note that calling `os.Exit()` or unhandled panics **does not execute your shutdown handlers**.

Also there are some things to be mindful of:

* Notifiers **can** be created inside shutdown code, but only for stages **following** the current. So stage 1 notifiers can create stage 2 notifiers, but if they create a stage 1 notifier this will never be called.
* Timeout can be changed once shutdown has been initiated, but it will only affect the **following** stages.
* Notifiers returned from a function (eg. FirstFn) can be used for selects. They will be notified, but the shutdown manager will not wait for them to finish, so using them for this is not recommended.
* If a panic occurs inside a shutdown function call in your code, the panic will be recovered and **ignored** and the shutdown will proceed. A message along with the backtrace is printed to `Logger`. If you want to handle panics, you must do it in your code.
* When shutdown is initiated, it cannot be stopped.

When you design with this do take care that this library is for **controlled** shutdown of your application. If you application crashes no shutdown handlers are run, so panics will still be fatal. You can of course still call the `m.Shutdown()` function if you recover a panic, but the library does nothing like this automatically.

## nil notifiers

It was tricky to detect cases where shutdown had started when you requested notifiers.

To help for that common case, the library now *returns a nil Notifier* if shutdown has already
reached the stage you are requesting a notifier for.

This is backwards compatible, but makes it much easier to test for such a case:

```Go
    f := shutdown.m.First()
    if f == nil {
        // Already shutting down.
        return
    }
```

## "context" support

Support for the [context](https://golang.org/pkg/context/) package has been added.
This allows you to easily wrap shutdown cancellation to your contexts using `shutdown.CancelCtx(parent Context)`.

This functions equivalent to calling [`context.WithCancel`](https://golang.org/pkg/context/#WithCancel) and
you must release resources the same way, by calling the returned
[CancelFunc](https://golang.org/pkg/context/#CancelFunc).

For legacy codebases we will seamlessly integrate with
[golang.org/x/net/context](https://godoc.org/golang.org/x/net/context).
Be sure to update to the latest version using `go get -u golang.org/x/net/context`,
since Go 1.7 compatibility is a recent update.

## logging

By default logging is done to the standard log package. You can replace the [Logger](https://godoc.org/github.com/eikmadsen/shutdown#pkg-variables) with your own before you start using the package. You can also send a "Printf" style function to the [`SetLogPrinter`](https://godoc.org/github.com/eikmadsen/shutdown#SetLogPrinter). This will allow you to easy hook up things like `(*testing.T).Logf` or specific loggers to intercept output.

You can set a custom `WarningPrefix` and `ErrorPrefix` in the [package variables](https://godoc.org/github.com/eikmadsen/shutdown#pkg-variables).

When you keep [`LogLockTimeout`](https://godoc.org/github.com/eikmadsen/shutdown#pkg-variables) enabled, you will also get detailed information about your lock timeouts, including a `file:line` indication where the notifier/lock was created. It is recommended to keep this enabled for easier debugging.

If a line number isn't enough information you can pass something that can identify your `shutdown.FirstFn(func() {select{}}, "Some Context")` or `shutdown.First("Some Context")`, will print "Some Context" when the function fails to return or the notifier isn't closed. The context is simply `fmt.Printf("%v", ctx)` when the function is created, so you can pass arbitrary objects.

You can use `SetLogPrinter(func(string, ...interface{}){})` to disable logging.

## why 3 stages?

By limiting the design to "only" three stages enable you to clearly make design choices, and force you to run as many things as possible in parallel. With this you can write simple design docs. Lets look at a webserver example:

* Preshutdown: Finish accepted requests, refuse new ones.
* Stage 1: Notify clients, flush data to database, notify upstream servers we are offline.
* Stage 2: Flush database bulk writers, messages, close databases. (no database writes)
* Stage 3: Flush/close log/metrics writer. (no log writes)

My intention is that this makes the shutdown process easier to manage, and encourage more concurrency, because you don't create a long daisy-chain of events, and doesn't force you to look through all your code to insert a single event correctly.

Don't think of the 3-stages as something that must do all stages of your shutdown. A single function call can of course (and is intended to) contain several "substages". Shutting down the database can easily be several stages, but you only register a single stage in the shutdown manager. The important part is that nothing else in the same stage can use the database.

## examples

There are examples in the [examples folder](https://github.com/eikmadsen/shutdown/tree/main/examples).

## license

This code is published under an MIT license. See LICENSE file for more information.
